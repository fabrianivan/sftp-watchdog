package service

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"

	"SFTPUpload/internal/config"
	"SFTPUpload/internal/logging"
	"SFTPUpload/internal/notifier"
	"SFTPUpload/internal/sftpclient"
	"SFTPUpload/internal/uploaded"
)

// UploadStat represents the progress of a single file upload.
type UploadStat struct {
	Filename string
	Speed    string
	Percent  float64 // 0.0 – 1.0
}

// ServiceEvent describes state changes the GUI should react to.
type ServiceEvent struct {
	Type    string // "status", "scan_start", "scan_end", "upload_start", "upload_done", "upload_fail", "error"
	Message string
}

// Service orchestrates scan/upload/backup operations.
type Service struct {
	cfg        *config.Config
	srcMgr     *sftpclient.Manager
	dstMgr     *sftpclient.Manager
	uploaded   *uploaded.Files
	notifier   notifier.Notifier
	logger     *logging.Logger
	scanNowCh  chan struct{}
	scanMu     sync.Mutex

	StatsCh  chan UploadStat
	EventsCh chan ServiceEvent

	running   bool
	runningMu sync.Mutex
	stopCh    chan struct{}
}

// New creates a new Service.
func New(cfg *config.Config, srcMgr, dstMgr *sftpclient.Manager, uploaded *uploaded.Files, notifier notifier.Notifier, logger *logging.Logger) *Service {
	return &Service{
		cfg:       cfg,
		srcMgr:    srcMgr,
		dstMgr:    dstMgr,
		uploaded:  uploaded,
		notifier:  notifier,
		logger:    logger,
		scanNowCh: make(chan struct{}, 4),
		StatsCh:   make(chan UploadStat, 64),
		EventsCh:  make(chan ServiceEvent, 64),
	}
}

// ScanNow triggers an immediate scan from the GUI.
func (s *Service) ScanNow() {
	select {
	case s.scanNowCh <- struct{}{}:
	default:
	}
}

// IsRunning reports whether the scanner loop is active.
func (s *Service) IsRunning() bool {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	return s.running
}

// Start begins the scheduled scan loop in the background.
func (s *Service) Start() {
	s.runningMu.Lock()
	if s.running {
		s.runningMu.Unlock()
		return
	}
	s.stopCh = make(chan struct{})
	s.running = true
	s.runningMu.Unlock()

	go s.scheduleScans(s.stopCh)
	s.emitEvent("status", "Scanner started")
}

// Stop halts the scheduled scan loop.
func (s *Service) Stop() {
	s.runningMu.Lock()
	if !s.running {
		s.runningMu.Unlock()
		return
	}
	close(s.stopCh)
	s.running = false
	s.runningMu.Unlock()

	s.emitEvent("status", "Scanner stopped")
}

// RunImmediateScan performs a single synchronous scan.
func (s *Service) RunImmediateScan() {
	s.runScan(false)
}

func (s *Service) emitEvent(typ, msg string) {
	select {
	case s.EventsCh <- ServiceEvent{Type: typ, Message: msg}:
	default:
	}
}

func (s *Service) emitStat(stat UploadStat) {
	select {
	case s.StatsCh <- stat:
	default:
	}
}

func (s *Service) scheduleScans(stopCh <-chan struct{}) {
	interval := time.Duration(s.cfg.PollInterval) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var idleCount int
	connected := true

	for {
		select {
		case <-stopCh:
			s.logger.Write("Stopping scheduled scans")
			return

		case <-ticker.C:
			go func() {
				defer func() {
					if r := recover(); r != nil {
						s.logger.Write("PANIC in scheduled scan: %v\n%s", r, debug.Stack())
					}
				}()

				if s.cfg.ActiveSchedule.Enabled && !isWithinActiveSchedule(s.cfg.ActiveSchedule, s.logger) {
					s.logger.Write("Outside active schedule window → skipping scan.")
					return
				}

				s.scanMu.Lock()
				defer s.scanMu.Unlock()

				start := time.Now()
				s.logger.Write("Scheduled scan starting (interval %s)", interval)
				s.emitEvent("scan_start", "Scheduled scan starting")

				if !connected {
					s.logger.Write("Reconnecting SFTP sessions...")
					if err := s.srcMgr.Connect(); err != nil {
						s.logger.Write("ERROR: Failed to reconnect Source SFTP: %v", err)
					}
					if s.dstMgr != nil {
						if err := s.dstMgr.Connect(); err != nil {
							s.logger.Write("ERROR: Failed to reconnect Destination SFTP: %v", err)
						}
					}
					connected = true
				}

				newFiles := s.runScan(true)

				if newFiles == 0 {
					idleCount++
					s.logger.Write("No new files detected (%d/%d idle scans)", idleCount, s.cfg.MaxIdleScans)
				} else {
					if idleCount > 0 {
						s.logger.Write("Resetting idle counter — new files detected (%d)", newFiles)
					}
					idleCount = 0
				}

				if idleCount >= s.cfg.MaxIdleScans && connected {
					s.logger.Write("No new files after %d scans → disconnecting idle SFTP sessions", s.cfg.MaxIdleScans)
					s.srcMgr.Close()
					if s.dstMgr != nil {
						s.dstMgr.Close()
					}
					connected = false
					idleCount = 0
					s.notifier.Notify("SFTP Idle Disconnect",
						fmt.Sprintf("No new files detected after %d scans. Connection closed.", s.cfg.MaxIdleScans), 5)
				}

				s.emitEvent("scan_end", fmt.Sprintf("Scan finished in %s, %d new files", time.Since(start).Round(time.Second), newFiles))
				s.logger.Write("Scheduled scan finished in %s", time.Since(start).Round(time.Second))
			}()

		case <-s.scanNowCh:
			go func() {
				defer func() {
					if r := recover(); r != nil {
						s.logger.Write("PANIC in manual scan: %v\n%s", r, debug.Stack())
					}
				}()

				if !s.tryLockScan() {
					s.logger.Write("Manual scan skipped — another scan already running.")
					s.notifier.Notify("Scan Busy", "A scan is already in progress.", 5)
					return
				}
				defer s.scanMu.Unlock()

				s.logger.Write("Manual scan starting...")
				s.emitEvent("scan_start", "Manual scan starting")
				s.notifier.Notify("Manual Scan", "Manual scan started...", 5)

				if !connected {
					s.logger.Write("Reconnecting before manual scan...")
					if err := s.srcMgr.Connect(); err != nil {
						s.logger.Write("ERROR: Failed to reconnect Source SFTP: %v", err)
					}
					if s.dstMgr != nil {
						if err := s.dstMgr.Connect(); err != nil {
							s.logger.Write("ERROR: Failed to reconnect Destination SFTP: %v", err)
						}
					}
					connected = true
				}

				newFiles := s.runScan(false)
				if newFiles > 0 {
					idleCount = 0
				}

				s.emitEvent("scan_end", fmt.Sprintf("Manual scan completed, %d new files", newFiles))
				s.logger.Write("Manual scan finished")
				s.notifier.Notify("Manual Scan", "Manual scan completed.", 5)
			}()
		}
	}
}

func isWithinActiveSchedule(s config.ScheduleConfig, logger *logging.Logger) bool {
	if !s.Enabled {
		return true
	}

	loc := time.Local
	if s.Timezone != "" && s.Timezone != "Local" {
		if tz, err := time.LoadLocation(s.Timezone); err == nil {
			loc = tz
		}
	}

	now := time.Now().In(loc)

	startTime, err1 := time.ParseInLocation("15:04", s.Start, loc)
	endTime, err2 := time.ParseInLocation("15:04", s.End, loc)
	if err1 != nil || err2 != nil {
		logger.Write("Invalid schedule time format (Start=%s End=%s)", s.Start, s.End)
		return true
	}

	start := time.Date(now.Year(), now.Month(), now.Day(), startTime.Hour(), startTime.Minute(), 0, 0, loc)
	end := time.Date(now.Year(), now.Month(), now.Day(), endTime.Hour(), endTime.Minute(), 0, 0, loc)

	if end.Before(start) {
		if now.Before(start) {
			start = start.Add(-24 * time.Hour)
		}
		end = end.Add(24 * time.Hour)
	}

	return now.After(start) && now.Before(end)
}

func (s *Service) runScan(async bool) int {
	s.logger.Write("Preparing to start scan...")

	if s.srcMgr.IsIdle() || !s.srcMgr.IsConnected() {
		s.logger.Write("Source SFTP idle/disconnected → reconnecting...")
		if err := s.srcMgr.Connect(); err != nil {
			s.logger.Write("Initial connect failed: %v → retrying...", err)
			if err := s.srcMgr.RetryConnect(); err != nil {
				s.logger.Write("ERROR: Cannot reconnect source: %v", err)
				return -1
			}
		}
	}

	client, err := s.srcMgr.GetClient()
	if err != nil {
		s.logger.Write("ERROR: Source client unavailable: %v", err)
		return -1
	}
	s.logger.Write("Source client ready")

	if s.dstMgr != nil && s.cfg.TargetSFTP.Host != "" {
		if s.dstMgr.IsIdle() || !s.dstMgr.IsConnected() {
			s.logger.Write("Destination SFTP idle/disconnected → reconnecting...")
			if err := s.dstMgr.Connect(); err != nil {
				if err := s.dstMgr.RetryConnect(); err != nil {
					s.logger.Write("WARNING: Cannot reconnect destination: %v", err)
					s.logger.Write("Switching to backup-only mode")
					s.dstMgr = nil
				}
			}
		}
	}

	sourceBase := filepath.ToSlash(s.cfg.SourceSFTP.TargetDirectory)
	if _, err := client.Stat(sourceBase); err != nil {
		s.logger.Write("ERROR: Cannot access source directory %s: %v", sourceBase, err)
		return -1
	}

	entries, err := client.ReadDir(sourceBase)
	if err != nil {
		s.logger.Write("ERROR: Failed to read source directory %s: %v", sourceBase, err)
		return -1
	}

	s.logger.Write("Scanning %s → %d entries", sourceBase, len(entries))
	newFiles := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".tmp") || strings.HasPrefix(name, ".") {
			continue
		}

		remotePath := filepath.ToSlash(filepath.Join(sourceBase, name))

		hash, err := sftpclient.ComputeRemoteFileHash(client, remotePath)
		if err != nil {
			s.logger.Write("WARNING: Cannot compute hash for %s: %v (skipping)", remotePath, err)
			continue
		}

		if s.uploaded.IsUploaded(remotePath, hash) {
			s.logger.Write("Already processed: %s (hash match)", remotePath)
			continue
		}

		newFiles++
		s.logger.Write("New file detected: %s (size=%d, hash=%s)", remotePath, entry.Size(), hash)

		if async {
			go s.processFile(remotePath)
		} else {
			s.processFile(remotePath)
		}
	}

	if newFiles == 0 {
		s.logger.Write("No new files found in %s", sourceBase)
	} else {
		s.logger.Write("Scan completed: %d new file(s) queued", newFiles)
	}
	return newFiles
}

func (s *Service) tryLockScan() bool {
	locked := make(chan bool, 1)
	go func() {
		s.scanMu.Lock()
		locked <- true
	}()
	select {
	case <-locked:
		return true
	case <-time.After(100 * time.Millisecond):
		return false
	}
}

func (s *Service) processFile(remotePath string) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Write("PANIC in processFile(%s): %v\n%s", remotePath, r, debug.Stack())
		}
	}()

	s.logger.Write("=== STARTING FILE PROCESSING: %s ===", remotePath)
	s.emitEvent("upload_start", filepath.Base(remotePath))

	srcClient, err := s.srcMgr.GetClient()
	if err != nil {
		s.logger.Write("ERROR: Cannot get source client for %s: %v", remotePath, err)
		s.emitEvent("upload_fail", filepath.Base(remotePath))
		return
	}

	fileInfo, err := srcClient.Stat(remotePath)
	if err != nil {
		s.logger.Write("ERROR: File %s no longer accessible: %v", remotePath, err)
		s.emitEvent("upload_fail", filepath.Base(remotePath))
		return
	}

	s.logger.Write("Processing %s (%d bytes)", remotePath, fileInfo.Size())

	hash, err := sftpclient.ComputeRemoteFileHash(srcClient, remotePath)
	if err != nil {
		s.logger.Write("ERROR: Failed to compute hash for %s: %v", remotePath, err)
		s.emitEvent("upload_fail", filepath.Base(remotePath))
		return
	}

	if s.uploaded.IsUploaded(remotePath, hash) {
		s.logger.Write("File already processed: %s", remotePath)
		return
	}

	fileName := filepath.Base(remotePath)
	copySuccess := false
	backupSuccess := false

	if s.dstMgr != nil && s.cfg.TargetSFTP.Host != "" {
		dstBase := filepath.ToSlash(s.cfg.TargetSFTP.TargetDirectory)
		remoteDstPath := filepath.ToSlash(filepath.Join(dstBase, fileName))

		// Check if destination already has identical file
		s.logger.Write("Checking destination for %s ...", remoteDstPath)
		dstClient, dstErr := s.dstMgr.GetClient()
		if dstErr == nil {
			if _, err := dstClient.Stat(remoteDstPath); err == nil {
				dstHash, err := sftpclient.ComputeRemoteFileHash(dstClient, remoteDstPath)
				if err == nil && dstHash == hash {
					s.logger.Write("Destination has identical file. Moving source to backup.")
					dateSubdir := time.Now().Format("2006-01-02")
					backupBase := filepath.ToSlash(s.cfg.BackupDirectory)
					backupDateDir := filepath.ToSlash(filepath.Join(backupBase, dateSubdir))
					backupPath := filepath.ToSlash(filepath.Join(backupDateDir, fileName))

					if err := s.srcMgr.MoveRemoteToBackup(remotePath, backupPath); err != nil {
						s.logger.Write("ERROR: Failed to move %s to backup: %v", remotePath, err)
					} else {
						if err := s.uploaded.MarkUploaded(remotePath, hash); err != nil {
							s.logger.Write("ERROR: Failed to mark %s as uploaded: %v", remotePath, err)
						}
						s.logger.Write("Marked %s as uploaded (skipped — identical file existed)", remotePath)
					}
					s.emitEvent("upload_done", fileName)
					s.logger.Write("=== COMPLETED FILE PROCESSING (skipped upload): %s ===", remotePath)
					return
				}
			}
		}

		// Copy file to destination
		s.logger.Write("COPYING: %s -> %s", remotePath, remoteDstPath)
		for attempt := 1; attempt <= s.cfg.ReconnectRetries+1; attempt++ {
			s.logger.Write("Copy attempt %d/%d", attempt, s.cfg.ReconnectRetries+1)

			duration, err := s.copyRemoteFileWithProgress(remotePath, remoteDstPath)
			if err == nil {
				s.logger.Write("SUCCESS: Copied %s in %s", fileName, duration)
				s.notifier.Notify("File Uploaded", fmt.Sprintf("%s uploaded in %s", fileName, duration.Round(time.Millisecond)), 5)
				copySuccess = true
				break
			}

			s.logger.Write("FAILED attempt %d: %v", attempt, err)

			if attempt <= s.cfg.ReconnectRetries {
				retryDelay := time.Duration(s.cfg.ReconnectInterval) * time.Second
				s.logger.Write("Retrying in %v...", retryDelay)
				time.Sleep(retryDelay)
				_ = s.srcMgr.RetryConnect()
				if s.dstMgr != nil {
					_ = s.dstMgr.RetryConnect()
				}
			} else {
				s.logger.Write("ALL COPY ATTEMPTS FAILED for %s", remotePath)
				s.notifier.Notify("Upload Failed", fmt.Sprintf("Failed to upload %s after %d attempts", fileName, s.cfg.ReconnectRetries+1), 5)
			}
		}

		// Move to backup on successful copy
		if copySuccess {
			dateSubdir := time.Now().Format("2006-01-02")
			backupBase := filepath.ToSlash(s.cfg.BackupDirectory)
			backupDateDir := filepath.ToSlash(filepath.Join(backupBase, dateSubdir))
			backupPath := filepath.ToSlash(filepath.Join(backupDateDir, fileName))

			s.logger.Write("MOVING TO BACKUP: %s -> %s", remotePath, backupPath)
			for attempt := 1; attempt <= s.cfg.ReconnectRetries+1; attempt++ {
				err := s.srcMgr.MoveRemoteToBackup(remotePath, backupPath)
				if err == nil {
					s.logger.Write("SUCCESS: Moved %s to backup", fileName)
					backupSuccess = true
					break
				}
				s.logger.Write("FAILED backup attempt %d: %v", attempt, err)
				if attempt <= s.cfg.ReconnectRetries {
					time.Sleep(time.Duration(s.cfg.ReconnectInterval) * time.Second)
					_ = s.srcMgr.RetryConnect()
				}
			}
		}
	} else {
		// No destination — backup only mode
		s.logger.Write("No destination configured; moving to backup only.")
		dateSubdir := time.Now().Format("2006-01-02")
		backupBase := filepath.ToSlash(s.cfg.BackupDirectory)
		backupDateDir := filepath.ToSlash(filepath.Join(backupBase, dateSubdir))
		backupPath := filepath.ToSlash(filepath.Join(backupDateDir, fileName))
		if err := s.srcMgr.MoveRemoteToBackup(remotePath, backupPath); err != nil {
			s.logger.Write("ERROR: Failed to move %s to backup: %v", remotePath, err)
		} else {
			backupSuccess = true
			_ = s.uploaded.MarkUploaded(remotePath, hash)
		}
	}

	if copySuccess && backupSuccess {
		if err := s.uploaded.MarkUploaded(remotePath, hash); err != nil {
			s.logger.Write("ERROR: Failed to mark %s as uploaded: %v", remotePath, err)
		} else {
			s.logger.Write("SUCCESS: Marked %s as uploaded", remotePath)
		}
		s.emitEvent("upload_done", fileName)
	} else {
		s.logger.Write("File %s not fully processed (copy: %v, backup: %v)", fileName, copySuccess, backupSuccess)
		s.emitEvent("upload_fail", fileName)
	}

	s.logger.Write("=== COMPLETED FILE PROCESSING: %s ===", remotePath)
}

func (s *Service) copyRemoteFileWithProgress(remoteSrcPath, remoteDstPath string) (time.Duration, error) {
	start := time.Now()

	srcClient, err := s.srcMgr.GetClient()
	if err != nil {
		return 0, err
	}
	dstClient, err := s.dstMgr.GetClient()
	if err != nil {
		return 0, err
	}

	s.srcMgr.UpdateActivity()
	s.dstMgr.UpdateActivity()

	srcFile, err := srcClient.Open(remoteSrcPath)
	if err != nil {
		return 0, fmt.Errorf("open source remote file=%s err=%v", remoteSrcPath, err)
	}
	defer srcFile.Close()

	info, err := srcFile.Stat()
	if err != nil {
		return 0, fmt.Errorf("stat source file=%s err=%v", remoteSrcPath, err)
	}

	remoteDstDir := filepath.ToSlash(filepath.Dir(remoteDstPath))
	if err := dstClient.MkdirAll(remoteDstDir); err != nil {
		return 0, fmt.Errorf("mkdir destination dir=%s err=%v", remoteDstDir, err)
	}

	timestamp := time.Now().Format("20060102_150405_000")
	baseName := filepath.Base(remoteDstPath)
	tmpDst := filepath.ToSlash(filepath.Join(remoteDstDir, "."+baseName+".tmp."+timestamp))

	s.logger.Write("Using temporary file: %s", tmpDst)

	dstFile, err := dstClient.OpenFile(tmpDst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
	if err != nil {
		return 0, fmt.Errorf("open destination tmp=%s err=%v", tmpDst, err)
	}

	// Use a progress-reporting writer
	filename := filepath.Base(remoteSrcPath)
	pw := &progressWriter{
		total:    info.Size(),
		filename: filename,
		service:  s,
		start:    start,
	}

	multiWriter := io.MultiWriter(dstFile, pw)
	_, err = io.Copy(multiWriter, srcFile)
	if err != nil {
		dstFile.Close()
		_ = dstClient.Remove(tmpDst)
		return 0, fmt.Errorf("copy error src=%s dst=%s err=%v", remoteSrcPath, remoteDstPath, err)
	}

	if err := dstFile.Close(); err != nil {
		_ = dstClient.Remove(tmpDst)
		return 0, fmt.Errorf("close destination file err=%v", err)
	}

	// Verify size
	tmpInfo, err := dstClient.Stat(tmpDst)
	if err != nil {
		_ = dstClient.Remove(tmpDst)
		return 0, fmt.Errorf("cannot stat temp file after copy: %v", err)
	}
	if tmpInfo.Size() != info.Size() {
		_ = dstClient.Remove(tmpDst)
		return 0, fmt.Errorf("temp file size mismatch: expected %d, got %d", info.Size(), tmpInfo.Size())
	}

	// Remove existing destination file if present
	if _, err := dstClient.Stat(remoteDstPath); err == nil {
		s.logger.Write("Destination file exists, removing: %s", remoteDstPath)
		_ = dstClient.Remove(remoteDstPath)
	}

	// Rename with retries
	var renameErr error
	for attempt := 1; attempt <= 3; attempt++ {
		renameErr = dstClient.Rename(tmpDst, remoteDstPath)
		if renameErr == nil {
			break
		}
		s.logger.Write("Rename attempt %d failed: %v", attempt, renameErr)
		if attempt < 3 {
			time.Sleep(2 * time.Second)
			if newClient, err := s.dstMgr.GetClient(); err == nil {
				dstClient = newClient
			}
		}
	}
	if renameErr != nil {
		_ = dstClient.Remove(tmpDst)
		return 0, fmt.Errorf("rename tmp=%s to final=%s err=%v", tmpDst, remoteDstPath, renameErr)
	}

	// Final verification
	finalInfo, err := dstClient.Stat(remoteDstPath)
	if err != nil {
		return 0, fmt.Errorf("cannot verify final file: %v", err)
	}
	if finalInfo.Size() != info.Size() {
		return 0, fmt.Errorf("final file size mismatch: expected %d, got %d", info.Size(), finalInfo.Size())
	}

	s.srcMgr.UpdateActivity()
	s.dstMgr.UpdateActivity()

	dur := time.Since(start)
	s.logger.Write("Copied %s -> %s size=%d duration=%s", remoteSrcPath, remoteDstPath, info.Size(), dur)

	// Send 100% stat
	s.emitStat(UploadStat{Filename: filename, Speed: sftpclient.TransferSpeed(info.Size(), int64(dur.Seconds())), Percent: 1.0})

	return dur, nil
}

// progressWriter implements io.Writer and emits UploadStat events periodically.
type progressWriter struct {
	written  int64
	total    int64
	filename string
	service  *Service
	start    time.Time
	lastEmit time.Time
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n := len(p)
	pw.written += int64(n)

	now := time.Now()
	if now.Sub(pw.lastEmit) >= 500*time.Millisecond {
		elapsed := now.Sub(pw.start).Seconds()
		var speed string
		if elapsed > 0 {
			speed = sftpclient.TransferSpeed(pw.written, int64(elapsed))
		} else {
			speed = "0 B/s"
		}
		percent := float64(pw.written) / float64(pw.total)
		if percent > 1.0 {
			percent = 1.0
		}
		pw.service.emitStat(UploadStat{Filename: pw.filename, Speed: speed, Percent: percent})
		pw.lastEmit = now
	}

	return n, nil
}

func setupBackupDirectory(client *sftp.Client, cfg *config.Config, logger *logging.Logger) error {
	backupBase := filepath.ToSlash(cfg.BackupDirectory)
	dateSubdir := time.Now().Format("2006-01-02")
	backupDateDir := filepath.ToSlash(filepath.Join(backupBase, dateSubdir))

	logger.Write("Verifying backup date directory: %s", backupDateDir)
	if err := sftpclient.EnsureDirWritable(client, backupDateDir, logger); err != nil {
		return fmt.Errorf("backup date directory %s not writable: %v", backupDateDir, err)
	}

	logger.Write("Backup directory %s is ready", backupDateDir)
	return nil
}

// PrepareDirectories creates source and backup directories on the SFTP servers.
func (s *Service) PrepareDirectories() error {
	srcBase := filepath.ToSlash(s.cfg.SourceSFTP.TargetDirectory)
	backupBase := filepath.ToSlash(s.cfg.BackupDirectory)

	s.logger.Write("Setting up source directory: %s", srcBase)
	if client, err := s.srcMgr.GetClient(); err == nil {
		if err := client.MkdirAll(srcBase); err != nil {
			s.logger.Write("WARNING: Failed to create source directory %s: %v", srcBase, err)
		} else {
			s.logger.Write("Source directory %s is ready", srcBase)
		}

		s.logger.Write("Setting up backup directory: %s", backupBase)
		if err := setupBackupDirectory(client, s.cfg, s.logger); err != nil {
			return err
		}
	}
	return nil
}

// TestConnections verifies SFTP connectivity to source and destination.
func (s *Service) TestConnections() {
	s.logger.Write("Testing source SFTP connection...")
	testSFTPConnection(s.srcMgr, "Source", s.logger)
	if s.dstMgr != nil {
		s.logger.Write("Testing destination SFTP connection...")
		testSFTPConnection(s.dstMgr, "Destination", s.logger)
	}

	s.logger.Write("Testing source SFTP capabilities...")
	testSFTPCapabilities(s.srcMgr, "Source", s.logger)
	if s.dstMgr != nil {
		s.logger.Write("Testing destination SFTP capabilities...")
		testSFTPCapabilities(s.dstMgr, "Destination", s.logger)
	}
}

// SourceConnected reports whether the source SFTP is currently connected.
func (s *Service) SourceConnected() bool {
	return s.srcMgr.IsConnected()
}

// DestConnected reports whether the destination SFTP is currently connected.
func (s *Service) DestConnected() bool {
	if s.dstMgr == nil {
		return false
	}
	return s.dstMgr.IsConnected()
}

// CloseAll tears down both SFTP connections and saves upload history.
func (s *Service) CloseAll() {
	s.logger.Write("Closing SFTP connections...")
	s.srcMgr.Close()
	if s.dstMgr != nil {
		s.dstMgr.Close()
	}
	if err := s.uploaded.Save(); err != nil {
		s.logger.Write("WARNING: Failed to save upload history: %v", err)
	}
	s.logger.Write("=== Program exited gracefully ===")
}

// ConnectAll establishes connections to source and (optionally) destination SFTP.
func (s *Service) ConnectAll() error {
	if err := s.srcMgr.Connect(); err != nil {
		s.logger.Write("ERROR: Source SFTP connect failed: %v", err)
		if err := s.srcMgr.RetryConnect(); err != nil {
			s.logger.Write("ERROR: Failed to reconnect source: %v", err)
			return fmt.Errorf("source SFTP: %w", err)
		}
	} else {
		s.logger.Write("Source SFTP connected successfully")
	}

	if s.dstMgr != nil {
		if err := s.dstMgr.Connect(); err != nil {
			s.logger.Write("ERROR: Destination SFTP connect failed: %v", err)
			if err := s.dstMgr.RetryConnect(); err != nil {
				s.logger.Write("ERROR: Failed to reconnect destination: %v", err)
				// Not fatal — can still operate in backup-only mode
			}
		} else {
			s.logger.Write("Destination SFTP connected successfully")
		}
	}

	return nil
}

func testSFTPConnection(mgr *sftpclient.Manager, name string, logger *logging.Logger) {
	client, err := mgr.GetClient()
	if err != nil {
		logger.Write("ERROR: %s SFTP connection test failed: %v", name, err)
		return
	}

	wd, err := client.Getwd()
	if err != nil {
		logger.Write("ERROR: %s SFTP cannot get working directory: %v", name, err)
		return
	}

	logger.Write("%s SFTP connection test successful - working directory: %s", name, wd)
}

func testSFTPCapabilities(mgr *sftpclient.Manager, name string, logger *logging.Logger) {
	defer func() {
		if r := recover(); r != nil {
			logger.Write("PANIC in testSFTPCapabilities(%s): %v\n%s", name, r, debug.Stack())
		}
	}()

	client, err := mgr.GetClient()
	if err != nil {
		logger.Write("ERROR: Cannot test %s SFTP capabilities: %v", name, err)
		return
	}

	testDir := "/tmp/sftp_test_" + fmt.Sprintf("%d", time.Now().Unix())

	if err := client.Mkdir(testDir); err != nil {
		logger.Write("%s SFTP: Directory creation test failed: %v", name, err)
	} else {
		logger.Write("%s SFTP: Directory creation test passed", name)
		_ = client.RemoveDirectory(testDir)
	}

	testFile := testDir + "/test.txt"
	f, err := client.Create(testFile)
	if err != nil {
		logger.Write("%s SFTP: File creation test failed: %v", name, err)
	} else {
		_, _ = f.Write([]byte("SFTP test"))
		f.Close()
		logger.Write("%s SFTP: File creation and write test passed", name)

		newTestFile := testFile + ".renamed"
		if err := client.Rename(testFile, newTestFile); err != nil {
			logger.Write("%s SFTP: File rename test failed: %v", name, err)
		} else {
			logger.Write("%s SFTP: File rename test passed", name)
			_ = client.Remove(newTestFile)
		}
	}

	if _, err := client.Stat("."); err != nil {
		logger.Write("%s SFTP: Stat operation test failed: %v", name, err)
	} else {
		logger.Write("%s SFTP: Stat operation test passed", name)
	}

	logger.Write("%s SFTP capabilities test completed", name)
}
