package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Logger struct {
	basePath      string
	retentionDays int

	currentDate string
	logFile     *os.File
	writer      io.Writer
	mu          sync.Mutex
}

var defaultLogger *Logger

func Init(basePath string, retentionDays int) (*Logger, error) {
	l := &Logger{
		basePath:      basePath,
		retentionDays: retentionDays,
		currentDate:   time.Now().Format("2006-01-02"),
		writer:        os.Stdout,
	}
	if err := l.openDailyLogFile(); err != nil {
		return nil, err
	}
	defaultLogger = l
	go l.cleanupLoop()
	return l, nil
}

func (l *Logger) cleanupLoop() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "PANIC in log cleanup goroutine: %v\n", r)
		}
	}()

	for {
		time.Sleep(24 * time.Hour)
		l.cleanupOldLogs()
	}
}

func (l *Logger) openDailyLogFile() error {
	dir := filepath.Dir(l.basePath)
	if dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	ext := filepath.Ext(l.basePath)
	name := strings.TrimSuffix(filepath.Base(l.basePath), ext)
	datedName := fmt.Sprintf("%s_%s%s", name, l.currentDate, ext)
	fullPath := filepath.Join(dir, datedName)

	f, err := os.OpenFile(fullPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}

	l.mu.Lock()
	if l.logFile != nil {
		_ = l.logFile.Close()
	}
	l.logFile = f
	l.writer = f
	l.mu.Unlock()

	return nil
}

func (l *Logger) Write(format string, a ...interface{}) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "PANIC in logger.Write: %v\n", r)
		}
	}()

	now := time.Now()
	today := now.Format("2006-01-02")

	if today != l.currentDate {
		l.currentDate = today
		_ = l.openDailyLogFile()
	}

	ts := now.Format(time.RFC3339Nano)
	message := fmt.Sprintf(format, a...)
	line := fmt.Sprintf("%s %s\n", ts, message)

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.writer != nil {
		_, _ = l.writer.Write([]byte(line))
	} else {
		fmt.Print(line)
	}
}

func (l *Logger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.logFile != nil {
		_ = l.logFile.Sync()
		_ = l.logFile.Close()
		l.logFile = nil
		l.writer = os.Stdout
	}
}

func (l *Logger) cleanupOldLogs() {
	if l.retentionDays <= 0 {
		return
	}
	dir := filepath.Dir(l.basePath)
	if dir == "." {
		dir = "."
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	cutoff := time.Now().AddDate(0, 0, -l.retentionDays)
	base := strings.TrimSuffix(filepath.Base(l.basePath), filepath.Ext(l.basePath))

	for _, f := range files {
		name := f.Name()
		if !strings.HasPrefix(name, base+"_") {
			continue
		}

		datePart := strings.TrimSuffix(strings.TrimPrefix(name, base+"_"), filepath.Ext(name))
		t, err := time.Parse("2006-01-02", datePart)
		if err != nil {
			continue
		}

		if t.Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

func L() *Logger {
	return defaultLogger
}
