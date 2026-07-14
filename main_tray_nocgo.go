//go:build !windows && (!darwin || !cgo)

package main

import (
	"SFTPUpload/internal/config"
	"SFTPUpload/internal/logging"
	"SFTPUpload/internal/service"
)

// startTray is a no-op on unsupported platforms and on macOS builds without
// cgo. It consumes stats to write them to the logger for visibility.
func startTray(svc *service.Service, cfg *config.Config, logger *logging.Logger, statsCh <-chan service.UploadStat) {
	logger.Write("Tray disabled (built without cgo). Running headless.")
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Write("PANIC in headless stats listener: %v", r)
			}
		}()

		for stat := range statsCh {
			logger.Write("Upload stat: %s speed=%s percent=%.1f%%", stat.Filename, stat.Speed, stat.Percent*100)
		}
	}()
}
