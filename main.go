package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"

	"SFTPUpload/internal/config"
	"SFTPUpload/internal/logging"
	"SFTPUpload/internal/notifier"
	"SFTPUpload/internal/service"
	"SFTPUpload/internal/sftpclient"
	"SFTPUpload/internal/uploaded"
)

var version = "dev"

func main() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "FATAL PANIC: %v\n%s\n", r, debug.Stack())
			os.Exit(1)
		}
	}()

	scanOnce := flag.Bool("scan", false, "Run a single scan (synchronous) and exit; no tray")
	configPath := flag.String("config", "config.json", "Path to config file")
	noTray := flag.Bool("no-tray", false, "Run without tray")
	// simulate-upload: when set, send fake upload stats to exercise tray/logger without SFTP
	simulate := flag.Bool("simulate-upload", false, "Simulate an upload and show bandwidth stats in the tray (no SFTP)")
	flag.Parse()

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

	logger, err := logging.Init(cfg.LogFile, cfg.LogRetentionDays)
	if err != nil {
		fmt.Printf("Failed to initialize log: %v\n", err)
		return
	}
	defer logger.Close()

	progressOutput := io.Discard
	if *scanOnce {
		progressOutput = os.Stdout
	}

	notify := notifier.BeeepNotifier{}

	srcMgr := sftpclient.NewManager(cfg.SourceSFTP, cfg.ReconnectRetries, cfg.ReconnectInterval, cfg.IdleTimeoutSeconds, cfg.KeepAliveDuration, logger, notify)
	var dstMgr *sftpclient.Manager
	if cfg.TargetSFTP.Host != "" {
		dstMgr = sftpclient.NewManager(cfg.TargetSFTP, cfg.ReconnectRetries, cfg.ReconnectInterval, cfg.IdleTimeoutSeconds, cfg.KeepAliveDuration, logger, notify)
	}

	uploadedFiles := uploaded.New("uploaded.json")
	if err := uploadedFiles.Load(); err != nil {
		logger.Write("WARNING: Failed to load uploaded.json: %v", err)
		logger.Write("Falling back to in-memory upload tracking; uploaded.json will not be used.")
		uploadedFiles.DisablePersistence()
	} else {
		logger.Write("Loaded upload history with %d processed files", len(uploadedFiles.Files))
	}

	// channel for upload stats to be shown in tray
	statsCh := make(chan service.UploadStat, 64)
	svc := service.New(cfg, srcMgr, dstMgr, uploadedFiles, notify, logger, progressOutput, statsCh)

	// If requested, simulate an upload by sending incremental stats to the stats channel
	if *simulate {
		go func() {
			fn := "simulated-file.dat"
			for i := 0; i <= 100; i += 10 {
				speed := "0 B/s"
				if i > 0 {
					speed = fmt.Sprintf("%d KB/s", 50+i)
				}
				statsCh <- service.UploadStat{Filename: fn, Speed: speed, Percent: float64(i) / 100.0}
				time.Sleep(1 * time.Second)
			}
			// close channel after simulation
			close(statsCh)
		}()
	}

	logger.Write("=== SFTP Watchdog Starting (version %s) ===", version)

	// Defer cleanup: resources will be closed in reverse order
	defer func() {
		logger.Write("Closing SFTP connections...")
		srcMgr.Close()
		if dstMgr != nil {
			dstMgr.Close()
		}
		saveUploads(uploadedFiles, logger)
		logger.Write("=== Program exited gracefully ===")
	}()

	if err := srcMgr.Connect(); err != nil {
		logger.Write("ERROR: Source SFTP initial connect failed: %v", err)
		if err := srcMgr.RetryConnect(); err != nil {
			logger.Write("ERROR: Failed to reconnect source SFTP: %v", err)
			if *scanOnce {
				return
			}
		}
	} else {
		logger.Write("Source SFTP connection established successfully")
	}

	if dstMgr != nil {
		if err := dstMgr.Connect(); err != nil {
			logger.Write("ERROR: Destination SFTP initial connect failed: %v", err)
			if err := dstMgr.RetryConnect(); err != nil {
				logger.Write("ERROR: Failed to reconnect destination SFTP: %v", err)
				if *scanOnce {
					return
				}
			}
		} else {
			logger.Write("Destination SFTP connection established successfully")
		}
	}

	svc.TestConnections()
	if err := svc.PrepareDirectories(); err != nil {
		logger.Write("ERROR: Backup directory setup failed: %v", err)
	}

	if *scanOnce {
		logger.Write("=== SFTP Watchdog: SINGLE-SCAN MODE (version %s) ===", version)
		svc.RunImmediateScan()
		logger.Write("=== Single scan completed; exiting. ===")
		return
	}

	if cfg.EnableInitialSync == nil || *cfg.EnableInitialSync {
		time.Sleep(2 * time.Second)
		logger.Write("Running initial scan right after startup...")
		svc.RunImmediateScan()
	} else {
		logger.Write("Initial sync disabled by config → skipping startup scan.")
	}

	stopCh := make(chan struct{})
	svc.Start(stopCh)

	if !*noTray {
		startTray(svc, cfg, logger, statsCh)
		notify.Notify("SFTP Uploader Started", "File monitoring is now active and will run indefinitely", 5)
	}

	logger.Write("Service is now monitoring for files indefinitely")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	<-sigCh
	logger.Write("=== Termination signal received, stopping services... ===")
	close(stopCh)
	time.Sleep(2 * time.Second)
}

func openLogFile(path string, logger *logging.Logger) {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("cmd", "/C", "start", "", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}

	if err := cmd.Start(); err != nil {
		logger.Write("Failed to open log file: %v", err)
	}
}

func saveUploads(u *uploaded.Files, logger *logging.Logger) {
	if err := u.Save(); err != nil {
		logger.Write("WARNING: Failed to save upload history: %v", err)
	}
}
