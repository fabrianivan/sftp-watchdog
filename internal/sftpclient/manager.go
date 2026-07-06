package sftpclient

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"math"
	"net"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"SFTPUpload/internal/config"
	"SFTPUpload/internal/logging"
	"SFTPUpload/internal/notifier"
)

type Manager struct {
	cfg               config.SFTPConfig
	logger            *logging.Logger
	notifier          notifier.Notifier
	mu                sync.Mutex
	client            *sftp.Client
	sshConn           *ssh.Client
	lastActivity      time.Time
	sessionStart      time.Time
	retries           int
	interval          int
	idleTimeout       int
	keepAliveDuration time.Duration
	connectingMu      sync.Mutex
	uploadMu          sync.Mutex
}

func NewManager(cfg config.SFTPConfig, retries, interval, idleTimeout, keepAliveSeconds int, logger *logging.Logger, notifier notifier.Notifier) *Manager {
	return &Manager{
		cfg:               cfg,
		logger:            logger,
		notifier:          notifier,
		lastActivity:      time.Now(),
		retries:           retries,
		interval:          interval,
		idleTimeout:       idleTimeout,
		keepAliveDuration: time.Duration(keepAliveSeconds) * time.Second,
	}
}

func (m *Manager) AcquireUpload() { m.uploadMu.Lock() }
func (m *Manager) ReleaseUpload() { m.uploadMu.Unlock() }

func (m *Manager) IsConnected() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.client != nil
}

func (m *Manager) IsIdle() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client == nil {
		return true
	}
	return time.Since(m.lastActivity) > time.Duration(m.idleTimeout)*time.Second
}

func (m *Manager) Connect() error {
	m.connectingMu.Lock()
	defer m.connectingMu.Unlock()

	m.mu.Lock()
	if m.client != nil {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	var hostKeyCallback ssh.HostKeyCallback
	if m.cfg.ExpectedFingerprint != "" {
		hostKeyCallback = func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			actualFingerprint := ssh.FingerprintSHA256(key)
			if actualFingerprint != m.cfg.ExpectedFingerprint {
				return fmt.Errorf("host key fingerprint mismatch: expected %s, got %s", m.cfg.ExpectedFingerprint, actualFingerprint)
			}
			return nil
		}
	} else {
		hostKeyCallback = ssh.InsecureIgnoreHostKey()
	}

	sshConfig := &ssh.ClientConfig{
		User:            m.cfg.Username,
		Auth:            []ssh.AuthMethod{ssh.Password(m.cfg.Password)},
		HostKeyCallback: hostKeyCallback,
		Timeout:         15 * time.Second,
	}

	addr := fmt.Sprintf("%s:%d", m.cfg.Host, m.cfg.Port)
	start := time.Now()

	sshConn, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		m.logger.Write("ERROR ssh dial failed (%s): %v", addr, err)
		m.notifier.Notify("SFTP Connection Failed", fmt.Sprintf("Failed to connect to %s: %v", addr, err), 5)
		return err
	}

	client, err := sftp.NewClient(sshConn)
	if err != nil {
		_ = sshConn.Close()
		m.logger.Write("ERROR creating sftp client: %v", err)
		m.notifier.Notify("SFTP Connection Failed", fmt.Sprintf("Failed to create SFTP client: %v", err), 5)
		return err
	}

	m.mu.Lock()
	if m.client != nil {
		_ = client.Close()
		_ = sshConn.Close()
		m.mu.Unlock()
		m.logger.Write("Connect(): connection created but a client already exists; discarding new connection")
		return nil
	}

	m.sshConn = sshConn
	m.client = client
	m.lastActivity = time.Now()
	m.sessionStart = start
	m.mu.Unlock()

	m.notifier.Notify("SFTP Connected", fmt.Sprintf("Connected to %s", m.cfg.Host), 5)
	m.logger.Write("SFTP login successful to %s; connect_time=%s", m.cfg.Host, time.Since(start))

	go m.keepAliveLoop()
	return nil
}

func (m *Manager) keepAliveLoop() {
	defer func() {
		if r := recover(); r != nil {
			m.logger.Write("PANIC in keepAliveLoop for %s: %v\n%s", m.cfg.Host, r, debug.Stack())
		}
	}()

	interval := 30 * time.Second
	if m.keepAliveDuration > 0 {
		interval = m.keepAliveDuration
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		m.mu.Lock()
		sshConn := m.sshConn
		clientAlive := m.client != nil && sshConn != nil
		m.mu.Unlock()

		if !clientAlive {
			return
		}

		if sshConn != nil {
			_, _, err := sshConn.SendRequest("keepalive@openssh.com", true, nil)
			if err != nil {
				m.logger.Write("Keepalive failed for %s: %v (will reconnect soon)", m.cfg.Host, err)
				m.Close()
				go m.RetryConnect()
				return
			}
			m.logger.Write("Keepalive OK for %s", m.cfg.Host)
		}
	}
}

func (m *Manager) Close() {
	m.uploadMu.Lock()
	defer m.uploadMu.Unlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.client != nil {
		_ = m.client.Close()
		m.client = nil
	}
	if m.sshConn != nil {
		_ = m.sshConn.Close()
		m.sshConn = nil
	}
	if !m.sessionStart.IsZero() {
		duration := time.Since(m.sessionStart)
		m.logger.Write("SFTP session closed (%s); duration=%s", m.cfg.Host, duration)
		m.notifier.Notify("SFTP Disconnected", fmt.Sprintf("Connection to %s closed after %s.", m.cfg.Host, duration.Truncate(time.Second)), 5)
		m.sessionStart = time.Time{}
	}
}

func (m *Manager) UpdateActivity() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastActivity = time.Now()
}

func (m *Manager) GetClient() (*sftp.Client, error) {
	m.mu.Lock()
	c := m.client
	m.mu.Unlock()

	if c != nil {
		if _, err := c.Stat("."); err == nil {
			m.UpdateActivity()
			return c, nil
		} else {
			m.logger.Write("SFTP client appears dead (%s): %v", m.cfg.Host, err)
		}
		m.Close()
	}

	var attempts int
	for {
		attempts++
		if attempts > m.retries*3 {
			return nil, fmt.Errorf("exceeded maximum reconnect attempts (%d) for %s", attempts-1, m.cfg.Host)
		}

		err := m.RetryConnect()
		if err != nil {
			m.logger.Write("Reconnect failed (%s): %v. Retrying in %ds...", m.cfg.Host, err, m.interval)
			time.Sleep(time.Duration(m.interval) * time.Second)
			continue
		}

		m.mu.Lock()
		c = m.client
		m.mu.Unlock()

		if c == nil {
			m.logger.Write("Reconnect succeeded but client is nil? Retrying...")
			time.Sleep(time.Duration(m.interval) * time.Second)
			continue
		}

		if _, err := c.Stat("."); err != nil {
			m.logger.Write("New client test failed (%s): %v. Closing and retrying...", m.cfg.Host, err)
			m.Close()
			time.Sleep(time.Duration(m.interval) * time.Second)
			continue
		}

		m.logger.Write("SFTP connection healthy to %s", m.cfg.Host)
		m.UpdateActivity()
		return c, nil
	}
}

func (m *Manager) MoveFile(src, dst string) error {
	client, err := m.GetClient()
	if err != nil {
		return err
	}
	return client.Rename(src, dst)
}

func EnsureDirWritable(client *sftp.Client, dir string, logger *logging.Logger) error {
	dir = filepath.ToSlash(dir)
	if err := client.MkdirAll(dir); err != nil {
		logger.Write("ERROR: MkdirAll(%s) failed: %v", dir, err)
		return fmt.Errorf("mkdirall failed for %s: %w", dir, err)
	}

	testFile := filepath.ToSlash(filepath.Join(dir, fmt.Sprintf(".perm_test_%d.tmp", time.Now().UnixNano())))
	f, err := client.Create(testFile)
	if err != nil {
		logger.Write("ERROR: creating test file %s failed: %v", testFile, err)
		return fmt.Errorf("cannot create test file %s: %w", testFile, err)
	}
	defer func() {
		f.Close()
		if err := client.Remove(testFile); err != nil {
			logger.Write("WARNING: created test file %s but failed to remove: %v", testFile, err)
		}
	}()

	_, _ = f.Write([]byte("permtest"))

	logger.Write("Directory %s exists and is writable (test file created and removed).", dir)
	return nil
}

func (m *Manager) MoveRemoteToBackup(remotePath, backupPath string) error {
	client, err := m.GetClient()
	if err != nil {
		return err
	}

	backupPath = filepath.ToSlash(backupPath)
	backupDir := filepath.ToSlash(filepath.Dir(backupPath))

	if err := client.MkdirAll(backupDir); err != nil {
		m.logger.Write("ERROR: failed to create backup directory %s: %v", backupDir, err)
		return fmt.Errorf("failed to create backup directory %s: %w", backupDir, err)
	}

	if err := EnsureDirWritable(client, backupDir, m.logger); err != nil {
		m.logger.Write("ERROR: backup directory %s not writable: %v", backupDir, err)
		return err
	}

	originalBackupPath := backupPath
	counter := 1
	for {
		_, err := client.Stat(backupPath)
		if err != nil {
			break
		}
		ext := filepath.Ext(originalBackupPath)
		name := originalBackupPath[:len(originalBackupPath)-len(ext)]
		backupPath = fmt.Sprintf("%s_%d%s", name, counter, ext)
		counter++
	}

	if err := client.Rename(remotePath, backupPath); err != nil {
		m.logger.Write("ERROR: failed to move %s to backup %s: %v", remotePath, backupPath, err)
		return fmt.Errorf("failed to move %s to backup %s: %w", remotePath, backupPath, err)
	}

	m.logger.Write("Moved %s to backup %s", remotePath, backupPath)
	return nil
}

func (m *Manager) RetryConnect() error {
	m.connectingMu.Lock()
	defer m.connectingMu.Unlock()

	m.mu.Lock()
	client := m.client
	m.mu.Unlock()

	if client != nil {
		if _, err := client.Stat("."); err == nil {
			return nil
		}
		m.logger.Write("retryConnect: existing client is broken -> closing")
		m.Close()
	}

	retries := m.retries
	interval := m.interval

	var lastErr error
	for i := 0; i < retries; i++ {
		m.logger.Write("Reconnect attempt %d/%d to %s ...", i+1, retries, m.cfg.Host)

		if err := m.Connect(); err != nil {
			lastErr = err
			m.logger.Write("Reconnect attempt %d failed: %v", i+1, err)

			sleepSec := time.Duration(interval) * time.Second * time.Duration(1<<i)
			if sleepSec > 60*time.Second {
				sleepSec = 60 * time.Second
			}
			m.logger.Write("Retrying in %s...", sleepSec)
			time.Sleep(sleepSec)
			continue
		}

		m.mu.Lock()
		c := m.client
		m.mu.Unlock()

		if c == nil {
			lastErr = fmt.Errorf("connect succeeded but client is nil")
			time.Sleep(time.Duration(interval) * time.Second)
			continue
		}

		if _, err := c.Stat("."); err != nil {
			lastErr = err
			m.logger.Write("Post-connect stat failed: %v", err)
			m.Close()
			time.Sleep(time.Duration(interval) * time.Second)
			continue
		}

		m.logger.Write("Reconnect successful to %s", m.cfg.Host)
		return nil
	}

	return fmt.Errorf("all %d reconnect attempts failed: %v", retries, lastErr)
}

func (m *Manager) MonitorIdle(stopCh <-chan struct{}) {
	idle := time.Duration(m.idleTimeout) * time.Second
	if idle <= 0 {
		return
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			m.mu.Lock()
			last := m.lastActivity
			connected := m.client != nil
			m.mu.Unlock()
			if connected && time.Since(last) > idle {
				m.logger.Write("Idle timeout reached for %s (%s) -> closing connection", m.cfg.Host, time.Since(last))
				m.Close()
			}
		}
	}
}

func ComputeRemoteFileHash(client *sftp.Client, remotePath string) (string, error) {
	f, err := client.Open(remotePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

func TransferSpeed(bytes, windowSeconds int64) string {
	if windowSeconds <= 0 {
		return "0 B/s"
	}
	speedPerSec := float64(bytes) / float64(windowSeconds)
	switch {
	case speedPerSec < 1024:
		return fmt.Sprintf("%.1f B/s", speedPerSec)
	case speedPerSec < 1024*1024:
		return fmt.Sprintf("%.1f KB/s", speedPerSec/1024)
	default:
		return fmt.Sprintf("%.1f MB/s", speedPerSec/(1024*1024))
	}
}

func IsNaN(f float64) bool {
	return math.IsNaN(f)
}
