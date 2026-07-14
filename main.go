package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	"SFTPUpload/internal/config"
	"SFTPUpload/internal/logging"
	"SFTPUpload/internal/notifier"
	"SFTPUpload/internal/service"
	"SFTPUpload/internal/sftpclient"
	"SFTPUpload/internal/uploaded"
	"SFTPUpload/ui"
)

var version = "dev"

func main() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "FATAL PANIC: %v\n%s\n", r, debug.Stack())
			os.Exit(1)
		}
	}()

	scanOnce := flag.Bool("scan", false, "Run a single scan (headless) and exit")
	configPath := flag.String("config", "config.json", "Path to config file")
	flag.Parse()

	// Load or create config
	cfg, err := config.Load(*configPath)
	if err != nil {
		if os.IsNotExist(err) {
			if err := config.WriteDefaultConfig(*configPath); err != nil {
				fmt.Printf("Failed to create default config: %v\n", err)
			} else {
				fmt.Println("Created default config.json. Please edit it with your settings.")
			}
		} else {
			fmt.Printf("Failed to load config: %v\n", err)
		}
		return
	}

	// Initialize logger
	logger, err := logging.Init(cfg.LogFile, cfg.LogRetentionDays)
	if err != nil {
		fmt.Printf("Failed to initialize log: %v\n", err)
		return
	}
	defer logger.Close()

	logger.Write("=== SFTP Watchdog Starting (version %s) ===", version)

	// Create SFTP managers
	noopNotify := notifier.NoopNotifier{}
	srcMgr := sftpclient.NewManager(cfg.SourceSFTP, cfg.ReconnectRetries, cfg.ReconnectInterval, cfg.IdleTimeoutSeconds, cfg.KeepAliveDuration, logger, noopNotify)
	var dstMgr *sftpclient.Manager
	if cfg.TargetSFTP.Host != "" {
		dstMgr = sftpclient.NewManager(cfg.TargetSFTP, cfg.ReconnectRetries, cfg.ReconnectInterval, cfg.IdleTimeoutSeconds, cfg.KeepAliveDuration, logger, noopNotify)
	}

	// Load upload history
	uploadedFiles := uploaded.New("uploaded.json")
	if err := uploadedFiles.Load(); err != nil {
		logger.Write("WARNING: Failed to load uploaded.json: %v", err)
		logger.Write("Falling back to in-memory upload tracking")
		uploadedFiles.DisablePersistence()
	} else {
		logger.Write("Loaded upload history with %d processed files", len(uploadedFiles.Files))
	}

	// =========================================
	// Headless mode: single scan and exit
	// =========================================
	if *scanOnce {
		svc := service.New(cfg, srcMgr, dstMgr, uploadedFiles, noopNotify, logger)

		logger.Write("=== SFTP Watchdog: SINGLE-SCAN MODE ===")
		if err := svc.ConnectAll(); err != nil {
			logger.Write("ERROR: Failed to connect: %v", err)
			return
		}
		defer svc.CloseAll()

		svc.TestConnections()
		if err := svc.PrepareDirectories(); err != nil {
			logger.Write("ERROR: Backup directory setup failed: %v", err)
		}
		svc.RunImmediateScan()
		logger.Write("=== Single scan completed; exiting. ===")
		return
	}

	// =========================================
	// GUI mode: Fyne application
	// =========================================
	fyneApp := app.NewWithID("id.fabrianivan.sftpwatchdog")
	fyneApp.Settings().SetTheme(&ui.WatchdogTheme{})

	// Use Fyne notifier for GUI mode
	fyneNotify := &fyneDesktopNotifier{fyneApp: fyneApp}

	// Recreate managers with Fyne notifier
	srcMgr = sftpclient.NewManager(cfg.SourceSFTP, cfg.ReconnectRetries, cfg.ReconnectInterval, cfg.IdleTimeoutSeconds, cfg.KeepAliveDuration, logger, fyneNotify)
	if cfg.TargetSFTP.Host != "" {
		dstMgr = sftpclient.NewManager(cfg.TargetSFTP, cfg.ReconnectRetries, cfg.ReconnectInterval, cfg.IdleTimeoutSeconds, cfg.KeepAliveDuration, logger, fyneNotify)
	}

	svc := service.New(cfg, srcMgr, dstMgr, uploadedFiles, fyneNotify, logger)

	// Connect in background
	go func() {
		if err := svc.ConnectAll(); err != nil {
			logger.Write("WARNING: Initial connection failed: %v", err)
		}
		svc.TestConnections()
		if err := svc.PrepareDirectories(); err != nil {
			logger.Write("ERROR: Backup directory setup failed: %v", err)
		}

		// Auto-start if initial sync is enabled
		if cfg.EnableInitialSync == nil || *cfg.EnableInitialSync {
			time.Sleep(2 * time.Second)
			logger.Write("Running initial scan on startup...")
			svc.RunImmediateScan()
		}

		// Start the scanner
		svc.Start()
		logger.Write("Scanner started automatically")
	}()

	// Handle OS signals
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		<-sigCh
		logger.Write("=== Termination signal received ===")
		svc.Stop()
		svc.CloseAll()
		fyneApp.Quit()
	}()

	// Create and show GUI
	mainApp := ui.NewApp(fyneApp, svc, cfg, *configPath, logger, version)
	mainApp.Show()

	logger.Write("GUI application started")
	fyneApp.Run()

	// Cleanup after GUI exits
	svc.Stop()
	svc.CloseAll()
	logger.Write("=== Application exited ===")
}

// fyneDesktopNotifier implements notifier.Notifier using Fyne's notification API.
type fyneDesktopNotifier struct {
	fyneApp fyne.App
}

func (n *fyneDesktopNotifier) Notify(title, message string, _ int) {
	n.fyneApp.SendNotification(fyne.NewNotification("SFTP Watchdog — "+title, message))
}
