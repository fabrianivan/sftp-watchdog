package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SFTPConfig holds credentials and connection details for one SFTP endpoint.
type SFTPConfig struct {
	Host                string `json:"host"`
	Port                int    `json:"port"`
	Username            string `json:"username"`
	Password            string `json:"password"`
	TargetDirectory     string `json:"targetDirectory"`
	ExpectedFingerprint string `json:"expectedFingerprint"`
}

// ScheduleConfig defines the active scanning window.
type ScheduleConfig struct {
	Enabled  bool   `json:"enabled"`
	Start    string `json:"start"`
	End      string `json:"end"`
	Timezone string `json:"timezone"`
}

// Config is the top-level application configuration.
type Config struct {
	SourceSFTP         SFTPConfig     `json:"sourceSFTP"`
	TargetSFTP         SFTPConfig     `json:"targetSFTP"`
	BackupDirectory    string         `json:"backupDirectory"`
	IdleTimeoutSeconds int            `json:"idleTimeoutSeconds"`
	ReconnectInterval  int            `json:"reconnectInterval"`
	ReconnectRetries   int            `json:"reconnectRetries"`
	LogFile            string         `json:"logFile"`
	LogRetentionDays   int            `json:"logRetentionDays"`
	ShowBalloonTimeout int            `json:"showBalloonTimeout"`
	PollInterval       int            `json:"pollInterval"`
	ActiveSchedule     ScheduleConfig `json:"activeSchedule"`
	KeepAliveDuration  int            `json:"keepAliveDuration"`
	EnableInitialSync  *bool          `json:"enableInitialSync,omitempty"`
	MaxIdleScans       int            `json:"maxIdleScans"`
}

// Load reads and parses a config file, applies defaults, and validates.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	applyDefaults(&cfg)

	if err := validateSchedule(cfg.ActiveSchedule); err != nil {
		return nil, fmt.Errorf("validate schedule: %w", err)
	}

	return &cfg, nil
}

// Save writes the config to the given path as indented JSON.
func Save(cfg *Config, path string) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	dir := filepath.Dir(path)
	if dir != "." {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return fmt.Errorf("create config dir: %w", err)
		}
	}

	return os.WriteFile(path, data, 0600)
}

func applyDefaults(cfg *Config) {
	if cfg.SourceSFTP.Port == 0 {
		cfg.SourceSFTP.Port = 22
	}
	if cfg.TargetSFTP.Port == 0 {
		cfg.TargetSFTP.Port = 22
	}
	if cfg.IdleTimeoutSeconds == 0 {
		cfg.IdleTimeoutSeconds = 30
	}
	if cfg.ReconnectInterval == 0 {
		cfg.ReconnectInterval = 10
	}
	if cfg.ReconnectRetries == 0 {
		cfg.ReconnectRetries = 2
	}
	if cfg.LogFile == "" {
		cfg.LogFile = "sftp_uploader.log"
	}
	if cfg.LogRetentionDays == 0 {
		cfg.LogRetentionDays = 1
	}
	if cfg.ShowBalloonTimeout == 0 {
		cfg.ShowBalloonTimeout = 10
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 30
	}
	if cfg.KeepAliveDuration == 0 {
		cfg.KeepAliveDuration = 30
	}
	if cfg.MaxIdleScans == 0 {
		cfg.MaxIdleScans = 10
	}
	if cfg.ActiveSchedule == (ScheduleConfig{}) {
		cfg.ActiveSchedule = ScheduleConfig{
			Enabled:  false,
			Start:    "00:00",
			End:      "23:59",
			Timezone: "Local",
		}
	}
	if cfg.EnableInitialSync == nil {
		v := true
		cfg.EnableInitialSync = &v
	}
}

func validateSchedule(s ScheduleConfig) error {
	if !s.Enabled {
		return nil
	}

	start, err := time.ParseInLocation("15:04", s.Start, time.Local)
	if err != nil {
		return fmt.Errorf("invalid start time %q: %w", s.Start, err)
	}

	end, err := time.ParseInLocation("15:04", s.End, time.Local)
	if err != nil {
		return fmt.Errorf("invalid end time %q: %w", s.End, err)
	}

	if end.Before(start) {
		return fmt.Errorf("end time %q must be after start time %q", s.End, s.Start)
	}

	return nil
}

// DefaultConfig returns a Config with sensible placeholder values.
func DefaultConfig() *Config {
	return &Config{
		SourceSFTP: SFTPConfig{
			Host:            "localhost",
			Port:            22,
			Username:        "username",
			Password:        "password",
			TargetDirectory: "/files/ftp_folder/upload",
		},
		TargetSFTP: SFTPConfig{
			Host:            "localhost",
			Port:            22,
			Username:        "username",
			Password:        "password",
			TargetDirectory: "/upload",
		},
		BackupDirectory:    "/files/ftp_folder/backup",
		IdleTimeoutSeconds: 300,
		ReconnectInterval:  10,
		ReconnectRetries:   5,
		LogFile:            "sftp_uploader.log",
		LogRetentionDays:   7,
		ShowBalloonTimeout: 10,
		PollInterval:       30,
		MaxIdleScans:       10,
	}
}

// WriteDefaultConfig creates a default config file at the given path.
func WriteDefaultConfig(path string) error {
	return Save(DefaultConfig(), path)
}
