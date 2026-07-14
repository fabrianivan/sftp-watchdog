package ui

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"SFTPUpload/internal/logging"
)

const maxLogLines = 500

// LogViewer provides a real-time log viewing tab.
type LogViewer struct {
	container  *fyne.Container
	logList    *widget.List
	lines      []string
	autoScroll bool
	logPath    string
	logger     *logging.Logger
}

// NewLogViewer creates the Logs tab with real-time streaming and controls.
func NewLogViewer(logger *logging.Logger, logPath string) *LogViewer {
	lv := &LogViewer{
		lines:      make([]string, 0, maxLogLines),
		autoScroll: true,
		logPath:    logPath,
		logger:     logger,
	}

	lv.logList = widget.NewList(
		func() int {
			return len(lv.lines)
		},
		func() fyne.CanvasObject {
			label := widget.NewLabel("")
			label.Wrapping = fyne.TextWrapOff
			label.TextStyle = fyne.TextStyle{Monospace: true}
			return label
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			label := obj.(*widget.Label)
			if id < len(lv.lines) {
				label.SetText(lv.lines[id])
			}
		},
	)

	// Toolbar
	autoScrollCheck := widget.NewCheck("Auto-scroll", func(checked bool) {
		lv.autoScroll = checked
	})
	autoScrollCheck.SetChecked(true)

	clearBtn := widget.NewButtonWithIcon("Clear", theme.DeleteIcon(), func() {
		lv.lines = lv.lines[:0]
		lv.logList.Refresh()
	})

	openLogBtn := widget.NewButtonWithIcon("Open Log File", theme.FolderOpenIcon(), func() {
		openFile(logPath)
	})

	toolbar := container.NewHBox(
		autoScrollCheck,
		layout.NewSpacer(),
		clearBtn,
		openLogBtn,
	)

	lv.container = container.NewBorder(toolbar, nil, nil, nil, lv.logList)

	// Subscribe to log entries
	go lv.subscribeToLogs(logger)

	return lv
}

// Container returns the Fyne container for this tab.
func (lv *LogViewer) Container() *fyne.Container {
	return lv.container
}

func (lv *LogViewer) subscribeToLogs(logger *logging.Logger) {
	ch := logger.Subscribe()
	for entry := range ch {
		ts := entry.Time.Format(time.TimeOnly)
		line := fmt.Sprintf("[%s] %s", ts, entry.Message)

		lv.lines = append(lv.lines, line)

		// Trim to max
		if len(lv.lines) > maxLogLines {
			lv.lines = lv.lines[len(lv.lines)-maxLogLines:]
		}

		lv.logList.Refresh()

		if lv.autoScroll && len(lv.lines) > 0 {
			lv.logList.ScrollToBottom()
		}
	}
}

// AddLine appends a formatted line to the log viewer.
func (lv *LogViewer) AddLine(msg string) {
	ts := time.Now().Format(time.TimeOnly)
	line := fmt.Sprintf("[%s] %s", ts, msg)

	lv.lines = append(lv.lines, line)
	if len(lv.lines) > maxLogLines {
		lv.lines = lv.lines[len(lv.lines)-maxLogLines:]
	}

	lv.logList.Refresh()
	if lv.autoScroll && len(lv.lines) > 0 {
		lv.logList.ScrollToBottom()
	}
}

func openFile(path string) {
	// Sanitize path — only allow expected log file characters
	if strings.ContainsAny(path, "&|;`$") {
		return
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("cmd", "/C", "start", "", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}

	_ = cmd.Start()
}
