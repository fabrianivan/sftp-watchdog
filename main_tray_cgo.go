//go:build cgo

package main

import (
	"fmt"
	"os"

	"github.com/getlantern/systray"

	"SFTPUpload/assets"
	"SFTPUpload/internal/config"
	"SFTPUpload/internal/logging"
	"SFTPUpload/internal/service"
)

func startTray(svc *service.Service, cfg *config.Config, logger *logging.Logger, statsCh <-chan service.UploadStat) {
	go systray.Run(func() {
		systray.SetTitle("SFTP Uploader")
		systray.SetTooltip("File monitoring service")

		iconData, err := os.ReadFile("assets/logo.ico")
		if err != nil && len(assets.Logo) > 0 {
			iconData = assets.Logo
		}
		if len(iconData) > 0 {
			systray.SetIcon(iconData)
		}

		mScan := systray.AddMenuItem("Scan Now", "Run a scan immediately")
		mLogs := systray.AddMenuItem("Show Logs", "Open log file")
		systray.AddSeparator()
		// dynamic upload stats will be shown as menu items inserted below
		uploadItems := make(map[string]*systray.MenuItem)
		mExit := systray.AddMenuItem("Exit", "Quit the app")

		// listen for upload stats and update/create menu entries
		go func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Write("PANIC in tray stats listener: %v", r)
				}
			}()

			for stat := range statsCh {
				title := fmt.Sprintf("%s — %s — %.1f%%", stat.Filename, stat.Speed, stat.Percent*100)
				if it, ok := uploadItems[stat.Filename]; ok {
					it.SetTitle(title)
				} else {
					// create a new menu item for this upload
					it := systray.AddMenuItem(title, "Upload in progress")
					uploadItems[stat.Filename] = it
				}

				if stat.Percent >= 1.0 {
					// mark as complete
					if it, ok := uploadItems[stat.Filename]; ok {
						it.SetTitle(fmt.Sprintf("%s — done — %s", stat.Filename, stat.Speed))
					}
				}
			}
		}()

		for {
			select {
			case <-mScan.ClickedCh:
				logger.Write("Manual scan triggered from tray")
				svc.ScanNow()
			case <-mLogs.ClickedCh:
				openLogFile(cfg.LogFile, logger)
			case <-mExit.ClickedCh:
				logger.Write("Exit requested from tray")
				systray.Quit()
				return
			}
		}
	}, func() {
		logger.Write("Tray exited")
	})
}
