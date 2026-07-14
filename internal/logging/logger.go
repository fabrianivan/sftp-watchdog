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

// LogEntry represents a single log line with timestamp.
type LogEntry struct {
	Time    time.Time
	Message string
}

// Logger provides daily-rotated file logging with subscriber support for the GUI.
type Logger struct {
	basePath      string
	retentionDays int

	currentDate string
	logFile     *os.File
	writer      io.Writer
	mu          sync.Mutex

	subscribers   []chan LogEntry
	subscribersMu sync.RWMutex
}

var defaultLogger *Logger

// Init creates a new Logger with daily rotation and starts the cleanup goroutine.
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

// Subscribe returns a channel that receives all log entries in real time.
// The caller is responsible for reading from the channel to avoid blocking.
func (l *Logger) Subscribe() chan LogEntry {
	ch := make(chan LogEntry, 256)
	l.subscribersMu.Lock()
	l.subscribers = append(l.subscribers, ch)
	l.subscribersMu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber channel and closes it.
func (l *Logger) Unsubscribe(ch chan LogEntry) {
	l.subscribersMu.Lock()
	defer l.subscribersMu.Unlock()

	for i, sub := range l.subscribers {
		if sub == ch {
			l.subscribers = append(l.subscribers[:i], l.subscribers[i+1:]...)
			close(ch)
			return
		}
	}
}

func (l *Logger) notifySubscribers(entry LogEntry) {
	l.subscribersMu.RLock()
	defer l.subscribersMu.RUnlock()

	for _, ch := range l.subscribers {
		select {
		case ch <- entry:
		default:
			// Drop if subscriber is not keeping up
		}
	}
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
		if err := os.MkdirAll(dir, 0750); err != nil {
			return err
		}
	}

	ext := filepath.Ext(l.basePath)
	name := strings.TrimSuffix(filepath.Base(l.basePath), ext)
	datedName := fmt.Sprintf("%s_%s%s", name, l.currentDate, ext)
	fullPath := filepath.Join(dir, datedName)

	f, err := os.OpenFile(fullPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
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

// Write formats and writes a log line, rotating files daily and notifying subscribers.
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
	if l.writer != nil {
		_, _ = l.writer.Write([]byte(line))
	} else {
		fmt.Print(line)
	}
	l.mu.Unlock()

	// Notify GUI subscribers
	l.notifySubscribers(LogEntry{Time: now, Message: message})
}

// Close flushes and closes the underlying log file.
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
	if dir == "" {
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

// L returns the default logger instance.
func L() *Logger {
	return defaultLogger
}
