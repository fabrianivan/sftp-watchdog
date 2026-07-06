package service

import (
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"github.com/schollz/progressbar/v3"

	"SFTPUpload/internal/config"
	"SFTPUpload/internal/logging"
	"SFTPUpload/internal/notifier"
	"SFTPUpload/internal/sftpclient"
	"SFTPUpload/internal/uploaded"
)

type Service struct {
	cfg             *config.Config
	srcMgr          *sftpclient.Manager
	dstMgr          *sftpclient.Manager
	uploaded        *uploaded.Files
	notifier        notifier.Notifier
	logger          *logging.Logger
	progressOutput  io.Writer
	progressManager *progressBarManager
	scanNowCh       chan struct{}
	scanMu          sync.Mutex
}

type UploadStat struct {
	Filename string
	Speed    string
	Percent  float64 // 0.0 - 1.0
}

func New(cfg *config.Config, srcMgr, dstMgr *sftpclient.Manager, uploaded *uploaded.Files, notifier notifier.Notifier, logger *logging.Logger, progressOutput io.Writer, statsCh chan<- UploadStat) *Service {
	if progressOutput == nil {
		progressOutput = io.Discard
	}
	return &Service{
		cfg:             cfg,
		srcMgr:          srcMgr,
		dstMgr:          dstMgr,
		uploaded:        uploaded,
		notifier:        notifier,
		logger:          logger,
		progressOutput:  progressOutput,
		progressManager: newProgressBarManager(progressOutput, notifier, logger, statsCh),
		scanNowCh:       make(chan struct{}, 4),
	}
}

func (s *Service) ScanNow() {
	select {
	case s.scanNowCh <- struct{}{}:
	default:
	}
}

func (s *Service) Start(stopCh <-chan struct{}) {
	go s.scheduleScans(stopCh)
}

func (s *Service) RunImmediateScan() {
	s.runScan(false)
}

func (s *Service) scheduleScans(stopCh <-chan struct{}) {
	interval := time.Duration(s.cfg.PollInterval) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var idleCount int
	var connected = true

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

				if !connected {
					s.logger.Write("Reconnecting SFTP sessions (previously disconnected due to inactivity)...")
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
					s.logger.Write("No new files detected (%d/10 idle scans)", idleCount)
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
					s.notifier.Notify("SFTP Idle Disconnect", fmt.Sprintf("No new files detected after %d scans. Connection closed to save resources.", s.cfg.MaxIdleScans), 5)
				}

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
					s.notifier.Notify("Scan Busy", "A scan is already in progress. Please wait.", 5)
					return
				}
				defer s.scanMu.Unlock()

				s.logger.Write("Manual scan starting...")
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

				s.logger.Write("Manual scan finished")
				s.notifier.Notify("Manual Scan", "Manual scan completed successfully.", 5)
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
		s.logger.Write("Source SFTP idle/disconnected → reconnecting (connect first)...")
		if err := s.srcMgr.Connect(); err != nil {
			s.logger.Write("Initial connect failed for source: %v → trying retryConnect()", err)
			if err := s.srcMgr.RetryConnect(); err != nil {
				s.logger.Write("ERROR: Cannot reconnect source before scan: %v", err)
				return -1
			}
		}
	}

	client, err := s.srcMgr.GetClient()
	if err != nil {
		s.logger.Write("ERROR: Source client unavailable even after reconnect: %v", err)
		return -1
	}
	s.logger.Write("Source client ready, continuing scan...")

	if s.dstMgr != nil && s.cfg.TargetSFTP.Host != "" {
		if s.dstMgr.IsIdle() || !s.dstMgr.IsConnected() {
			s.logger.Write("Destination SFTP idle/disconnected → reconnecting (connect first)...")
			if err := s.dstMgr.Connect(); err != nil {
				s.logger.Write("Initial connect failed for destination: %v → trying retryConnect()", err)
				if err := s.dstMgr.RetryConnect(); err != nil {
					s.logger.Write("WARNING: Cannot reconnect destination SFTP: %v", err)
					s.logger.Write("Switching to backup-only mode for this scan")
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
			s.logger.Write("Already processed: %s (hash match in uploaded.json)", remotePath)
			continue
		}

		newFiles++
		s.logger.Write("New file detected: %s (size=%d, hash=%s)", remotePath, entry.Size(), hash)

		if async {
			go s.ProcessFile(remotePath)
		} else {
			s.ProcessFile(remotePath)
		}
	}

	if newFiles == 0 {
		s.logger.Write("No new files found in %s", sourceBase)
	} else {
		s.logger.Write("Scan completed: %d new file(s) queued for processing", newFiles)
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

func (s *Service) ProcessFile(remotePath string) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Write("PANIC in ProcessFile(%s): %v\n%s", remotePath, r, debug.Stack())
		}
	}()

	s.logger.Write("=== STARTING FILE PROCESSING: %s ===", remotePath)

	srcClient, err := s.srcMgr.GetClient()
	if err != nil {
		s.logger.Write("ERROR: Cannot get source client for %s: %v", remotePath, err)
		return
	}

	fileInfo, err := srcClient.Stat(remotePath)
	if err != nil {
		s.logger.Write("ERROR: File %s no longer accessible: %v", remotePath, err)
		return
	}

	s.logger.Write("Processing %s (%d bytes)", remotePath, fileInfo.Size())

	hash, err := sftpclient.ComputeRemoteFileHash(srcClient, remotePath)
	if err != nil {
		s.logger.Write("ERROR: Failed to compute hash for %s: %v", remotePath, err)
		return
	}

	s.logger.Write("Computed hash for %s: %s", remotePath, hash)

	if s.uploaded.IsUploaded(remotePath, hash) {
		s.logger.Write("File already processed (uploaded.json): %s", remotePath)
		return
	}

	fileName := filepath.Base(remotePath)
	copySuccess := false
	backupSuccess := false

	if s.dstMgr != nil && s.cfg.TargetSFTP.Host != "" {
		dstBase := filepath.ToSlash(s.cfg.TargetSFTP.TargetDirectory)
		remoteDstPath := filepath.ToSlash(filepath.Join(dstBase, fileName))

		s.logger.Write("Checking if destination has %s ...", remoteDstPath)
		dstClient, dstErr := s.dstMgr.GetClient()
		if dstErr == nil {
			if _, err := dstClient.Stat(remoteDstPath); err == nil {
				s.logger.Write("Destination file exists; computing hash to compare...")
				dstHash, err := sftpclient.ComputeRemoteFileHash(dstClient, remoteDstPath)
				if err == nil {
					s.logger.Write("Destination hash: %s", dstHash)
					if dstHash == hash {
						s.logger.Write("Destination file has identical hash. Moving source to backup and marking uploaded.")
						dateSubdir := time.Now().Format("2006-01-02")
						backupBase := filepath.ToSlash(s.cfg.BackupDirectory)
						backupDateDir := filepath.ToSlash(filepath.Join(backupBase, dateSubdir))
						backupPath := filepath.ToSlash(filepath.Join(backupDateDir, fileName))

						if err := s.srcMgr.MoveRemoteToBackup(remotePath, backupPath); err != nil {
							s.logger.Write("ERROR: Failed to move %s to backup: %v", remotePath, err)
						} else {
							backupSuccess = true
							if err := s.uploaded.MarkUploaded(remotePath, hash); err != nil {
								s.logger.Write("ERROR: Failed to mark %s as uploaded: %v", remotePath, err)
							} else {
								s.logger.Write("Marked %s as uploaded in uploaded.json (skipped upload because identical file existed)", remotePath)
							}
						}
						s.logger.Write("=== COMPLETED FILE PROCESSING (skipped upload): %s ===", remotePath)
						return
					}
				} else {
					s.logger.Write("WARNING: Could not compute dest file hash: %v", err)
				}
			}
		} else {
			s.logger.Write("WARNING: Could not access destination client to check existing file: %v", dstErr)
		}

		s.logger.Write("ATTEMPTING COPY: %s -> %s", remotePath, filepath.Join(s.cfg.TargetSFTP.TargetDirectory, fileName))

		for attempt := 1; attempt <= s.cfg.ReconnectRetries+1; attempt++ {
			s.logger.Write("Copy attempt %d/%d", attempt, s.cfg.ReconnectRetries+1)

			duration, err := s.copyRemoteFileWithTiming(remotePath, filepath.ToSlash(filepath.Join(s.cfg.TargetSFTP.TargetDirectory, fileName)))
			if err == nil {
				s.logger.Write("SUCCESS: Copied %s to destination in %s", fileName, duration)
				s.notifier.Notify("File Uploaded", fmt.Sprintf("File %s uploaded successfully in %s", fileName, duration.Round(time.Millisecond)), 5)
				copySuccess = true
				break
			}

			s.logger.Write("FAILED attempt %d: %v", attempt, err)

			if strings.Contains(err.Error(), "SSH_FX_FAILURE") {
				s.logger.Write("SFTP server returned SSH_FX_FAILURE - this may be a temporary server issue")
			}

			if attempt <= s.cfg.ReconnectRetries {
				retryDelay := time.Duration(s.cfg.ReconnectInterval) * time.Second
				s.logger.Write("Retrying in %v...", retryDelay)
				time.Sleep(retryDelay)

				if err := s.srcMgr.RetryConnect(); err != nil {
					s.logger.Write("Reconnect source failed: %v", err)
				}
				if err := s.dstMgr.RetryConnect(); err != nil {
					s.logger.Write("Reconnect destination failed: %v", err)
				}
			} else {
				s.logger.Write("ALL COPY ATTEMPTS FAILED for %s", remotePath)
				s.notifier.Notify("Upload Failed", fmt.Sprintf("Failed to upload %s after %d attempts", fileName, s.cfg.ReconnectRetries+1), 5)
			}
		}

		if copySuccess {
			dateSubdir := time.Now().Format("2006-01-02")
			backupBase := filepath.ToSlash(s.cfg.BackupDirectory)
			backupDateDir := filepath.ToSlash(filepath.Join(backupBase, dateSubdir))
			backupPath := filepath.ToSlash(filepath.Join(backupDateDir, fileName))

			s.logger.Write("MOVING TO BACKUP: %s -> %s (date-based subdirectory)", remotePath, backupPath)

			for attempt := 1; attempt <= s.cfg.ReconnectRetries+1; attempt++ {
				s.logger.Write("Backup move attempt %d/%d", attempt, s.cfg.ReconnectRetries+1)

				err := s.srcMgr.MoveRemoteToBackup(remotePath, backupPath)
				if err == nil {
					s.logger.Write("SUCCESS: Moved %s to backup %s", fileName, backupPath)
					s.notifier.Notify("File Moved to Backup", fmt.Sprintf("File %s moved to backup", fileName), 5)
					backupSuccess = true
					break
				}

				s.logger.Write("FAILED backup attempt %d: %v", attempt, err)

				if attempt <= s.cfg.ReconnectRetries {
					retryDelay := time.Duration(s.cfg.ReconnectInterval) * time.Second
					s.logger.Write("Retrying in %v...", retryDelay)
					time.Sleep(retryDelay)
					if err := s.srcMgr.RetryConnect(); err != nil {
						s.logger.Write("Reconnect source failed: %v", err)
					}
				} else {
					s.logger.Write("ALL BACKUP ATTEMPTS FAILED for %s", remotePath)
					s.notifier.Notify("Backup Failed", fmt.Sprintf("Failed to move %s to backup", fileName), 5)
				}
			}
		} else {
			s.logger.Write("Copy failed, skipping backup move for %s", remotePath)
		}
	} else {
		s.logger.Write("No destination SFTP configured; moving to backup only.")
		dateSubdir := time.Now().Format("2006-01-02")
		backupBase := filepath.ToSlash(s.cfg.BackupDirectory)
		backupDateDir := filepath.ToSlash(filepath.Join(backupBase, dateSubdir))
		backupPath := filepath.ToSlash(filepath.Join(backupDateDir, fileName))
		if err := s.srcMgr.MoveRemoteToBackup(remotePath, backupPath); err != nil {
			s.logger.Write("ERROR: Failed to move %s to backup: %v", remotePath, err)
		} else {
			backupSuccess = true
			if err := s.uploaded.MarkUploaded(remotePath, hash); err != nil {
				s.logger.Write("ERROR: Failed to mark %s as uploaded: %v", remotePath, err)
			}
		}
	}

	if copySuccess && backupSuccess {
		if err := s.uploaded.MarkUploaded(remotePath, hash); err != nil {
			s.logger.Write("ERROR: Failed to mark %s as uploaded: %v", remotePath, err)
		} else {
			s.logger.Write("SUCCESS: Marked %s as uploaded in JSON - will not be processed again", remotePath)
		}
	} else {
		s.logger.Write("File %s not marked as uploaded because operation was not fully successful (copy: %v, backup: %v)", fileName, copySuccess, backupSuccess)
	}

	s.logger.Write("=== COMPLETED FILE PROCESSING: %s ===", remotePath)
}

func (s *Service) copyRemoteFileWithTiming(remoteSrcPath, remoteDstPath string) (time.Duration, error) {
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

	filename := filepath.Base(remoteSrcPath)
	bar := s.progressManager.CreateBar(filename, info.Size())
	defer s.progressManager.RemoveBar(filename)

	multiWriter := io.MultiWriter(dstFile, bar)

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

	tmpInfo, err := dstClient.Stat(tmpDst)
	if err != nil {
		_ = dstClient.Remove(tmpDst)
		return 0, fmt.Errorf("cannot stat temp file after copy: %v", err)
	}
	if tmpInfo.Size() != info.Size() {
		_ = dstClient.Remove(tmpDst)
		return 0, fmt.Errorf("temp file size mismatch: expected %d, got %d", info.Size(), tmpInfo.Size())
	}

	if _, err := dstClient.Stat(remoteDstPath); err == nil {
		s.logger.Write("Destination file already exists, removing: %s", remoteDstPath)
		if err := dstClient.Remove(remoteDstPath); err != nil {
			s.logger.Write("WARNING: Could not remove existing destination file: %v", err)
		}
	}

	var renameErr error
	for attempt := 1; attempt <= 3; attempt++ {
		s.logger.Write("Rename attempt %d/3: %s -> %s", attempt, tmpDst, remoteDstPath)
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

	finalInfo, err := dstClient.Stat(remoteDstPath)
	if err != nil {
		return 0, fmt.Errorf("cannot verify final file after rename: %v", err)
	}
	if finalInfo.Size() != info.Size() {
		return 0, fmt.Errorf("final file size mismatch: expected %d, got %d", info.Size(), finalInfo.Size())
	}

	s.srcMgr.UpdateActivity()
	s.dstMgr.UpdateActivity()

	dur := time.Since(start)
	s.logger.Write("Copied %s -> %s size=%d duration=%s", remoteSrcPath, remoteDstPath, info.Size(), dur)
	return dur, nil
}

func setupBackupDirectory(client *sftp.Client, cfg *config.Config, logger *logging.Logger) error {
	backupBase := filepath.ToSlash(cfg.BackupDirectory)
	dateSubdir := time.Now().Format("2006-01-02")
	backupDateDir := filepath.ToSlash(filepath.Join(backupBase, dateSubdir))

	logger.Write("Verifying backup date directory: %s", backupDateDir)

	if err := sftpclient.EnsureDirWritable(client, backupDateDir, logger); err != nil {
		logger.Write("ERROR: Backup date directory %s not writable: %v", backupDateDir, err)
		return fmt.Errorf("backup date directory %s not writable: %v", backupDateDir, err)
	}

	logger.Write("Backup directory %s is ready and writable", backupDateDir)
	return nil
}

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
		client.RemoveDirectory(testDir)
	}

	testFile := testDir + "/test.txt"
	testContent := "SFTP test file content"

	f, err := client.Create(testFile)
	if err != nil {
		logger.Write("%s SFTP: File creation test failed: %v", name, err)
	} else {
		_, _ = f.Write([]byte(testContent))
		f.Close()
		logger.Write("%s SFTP: File creation and write test passed", name)

		newTestFile := testFile + ".renamed"
		if err := client.Rename(testFile, newTestFile); err != nil {
			logger.Write("%s SFTP: File rename test failed: %v", name, err)
		} else {
			logger.Write("%s SFTP: File rename test passed", name)
			client.Remove(newTestFile)
		}
	}

	if _, err := client.Stat("."); err != nil {
		logger.Write("%s SFTP: Stat operation test failed: %v", name, err)
	} else {
		logger.Write("%s SFTP: Stat operation test passed", name)
	}

	logger.Write("%s SFTP capabilities test completed", name)
}

type progressBarManager struct {
	mu      sync.Mutex
	bars    map[string]*progressbar.ProgressBar
	writer  io.Writer
	notify  notifier.Notifier
	logger  *logging.Logger
	statsCh chan<- UploadStat
}

func newProgressBarManager(writer io.Writer, notify notifier.Notifier, logger *logging.Logger, statsCh chan<- UploadStat) *progressBarManager {
	return &progressBarManager{
		bars:    make(map[string]*progressbar.ProgressBar),
		writer:  writer,
		notify:  notify,
		logger:  logger,
		statsCh: statsCh,
	}
}

func (p *progressBarManager) CreateBar(filename string, size int64) *progressbar.ProgressBar {
	p.mu.Lock()
	defer p.mu.Unlock()

	if existing, exists := p.bars[filename]; exists {
		existing.Close()
	}

	bar := progressbar.NewOptions64(
		size,
		progressbar.OptionSetDescription(fmt.Sprintf("Copying %s", truncateFilename(filename, 30))),
		progressbar.OptionSetWidth(30),
		progressbar.OptionShowBytes(true),
		progressbar.OptionShowCount(),
		progressbar.OptionSetWriter(p.writer),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "=",
			SaucerHead:    ">",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
		progressbar.OptionClearOnFinish(),
		progressbar.OptionThrottle(65*time.Millisecond),
		progressbar.OptionOnCompletion(func() {
			fmt.Fprint(p.writer, "\n")
		}),
	)

	if p.writer == io.Discard {
		go p.notifyProgress(filename, bar)
	}

	p.bars[filename] = bar
	return bar
}

func (p *progressBarManager) notifyProgress(filename string, bar *progressbar.ProgressBar) {
	defer func() {
		if r := recover(); r != nil {
			p.logger.Write("PANIC in notifyProgress(%s): %v\n%s", filename, r, debug.Stack())
		}
	}()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	var lastBytes int64
	var lastPercent float64
	var lastBalloon time.Time

	for range ticker.C {
		state := bar.State()
		current := state.CurrentPercent
		bytes := state.CurrentBytes

		if math.IsNaN(current) || current <= 0 {
			continue
		}

		speedBytes := int64(bytes) - lastBytes
		speedStr := sftpclient.TransferSpeed(speedBytes, 10)

		progressDelta := current - lastPercent
		if progressDelta >= 0.1 || time.Since(lastBalloon) > 30*time.Second {
			p.notify.Notify("Uploading "+filename, fmt.Sprintf("%.1f%% complete\nSpeed: %s", current*100, speedStr), 5)
			lastPercent = current
			lastBalloon = time.Now()
		}

		// send stats to tray (non-blocking)
		if p.statsCh != nil {
			select {
			case p.statsCh <- UploadStat{Filename: filename, Speed: speedStr, Percent: current}:
			default:
			}
		}

		if current >= 1.0 {
			p.notify.Notify("Upload Complete", fmt.Sprintf("%s finished successfully.\nAverage speed: %s", filename, speedStr), 5)
			return
		}

		lastBytes = int64(bytes)
	}
}

func (p *progressBarManager) RemoveBar(filename string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if bar, exists := p.bars[filename]; exists {
		bar.Close()
		delete(p.bars, filename)
	}
}

func truncateFilename(filename string, maxLength int) string {
	if len(filename) <= maxLength {
		return filename
	}
	ext := filepath.Ext(filename)
	name := filename[:len(filename)-len(ext)]
	if len(name) > maxLength-3-len(ext) {
		name = name[:maxLength-3-len(ext)] + "..."
	}
	return name + ext
}
