package ui

import (
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"SFTPUpload/internal/config"
	"SFTPUpload/internal/service"
)

// Dashboard provides the main status and control tab.
type Dashboard struct {
	container *fyne.Container
	svc       *service.Service
	cfg       *config.Config

	// Status indicators
	srcStatus     *canvas.Circle
	srcLabel      *widget.Label
	dstStatus     *canvas.Circle
	dstLabel      *widget.Label
	scannerStatus *widget.Label
	lastScanLabel *widget.Label
	nextScanLabel *widget.Label
	filesLabel    *widget.Label
	scheduleLabel *widget.Label

	// Controls
	toggleBtn *widget.Button
	scanBtn   *widget.Button

	// Progress area
	progressBox  *fyne.Container
	progressBars map[string]*widget.ProgressBar

	// Event display
	eventLabel *widget.Label

	// Counters
	filesProcessed int
}

// NewDashboard creates the dashboard tab.
func NewDashboard(svc *service.Service, cfg *config.Config) *Dashboard {
	d := &Dashboard{
		svc:          svc,
		cfg:          cfg,
		progressBars: make(map[string]*widget.ProgressBar),
	}

	d.buildUI()
	go d.listenEvents()
	go d.listenStats()
	go d.updateLoop()

	return d
}

// Container returns the Fyne container for this tab.
func (d *Dashboard) Container() *fyne.Container {
	return d.container
}

func (d *Dashboard) buildUI() {
	// === Header Section ===
	title := canvas.NewText("SFTP WATCHDOG", ColorIndigo)
	title.TextSize = 24
	title.TextStyle = fyne.TextStyle{Bold: true}

	subtitle := canvas.NewText("Automated File Transfer Monitor & Sync Utility", ColorMuted)
	subtitle.TextSize = 12

	var headerLogo fyne.CanvasObject
	logoImg := canvas.NewImageFromFile("assets/logo.png")
	if logoImg != nil {
		logoImg.FillMode = canvas.ImageFillContain
		logoImg.SetMinSize(fyne.NewSize(50, 50))
		headerLogo = logoImg
	} else {
		headerLogo = canvas.NewCircle(ColorIndigo)
		headerLogo.Resize(fyne.NewSize(50, 50))
	}

	headerText := container.NewVBox(title, subtitle)
	header := container.NewHBox(
		headerLogo,
		layout.NewSpacer(),
		headerText,
		layout.NewSpacer(),
	)

	// === Connection Status ===
	d.srcStatus = canvas.NewCircle(ColorMuted)
	d.srcStatus.Resize(fyne.NewSize(12, 12))
	d.srcLabel = widget.NewLabel("Source: Connecting...")
	d.srcLabel.TextStyle = fyne.TextStyle{Bold: true}

	d.dstStatus = canvas.NewCircle(ColorMuted)
	d.dstStatus.Resize(fyne.NewSize(12, 12))
	d.dstLabel = widget.NewLabel("Destination: Connecting...")
	d.dstLabel.TextStyle = fyne.TextStyle{Bold: true}

	srcRow := container.NewHBox(
		container.NewCenter(d.srcStatus),
		d.srcLabel,
	)
	dstRow := container.NewHBox(
		container.NewCenter(d.dstStatus),
		d.dstLabel,
	)

	connectionCard := widget.NewCard("Connection Status", "Monitor SFTP server connectivity", container.NewVBox(srcRow, dstRow))

	// === Scanner Status ===
	d.scannerStatus = widget.NewLabel("Scanner: Stopped")
	d.scannerStatus.TextStyle = fyne.TextStyle{Bold: true}

	d.lastScanLabel = widget.NewLabel("Last scan: —")
	d.nextScanLabel = widget.NewLabel("Poll interval: —")
	d.filesLabel = widget.NewLabel("Files processed: 0")
	d.scheduleLabel = widget.NewLabel("Schedule: —")
	d.eventLabel = widget.NewLabel("Waiting for action...")

	// Update schedule label
	if d.cfg.ActiveSchedule.Enabled {
		d.scheduleLabel.SetText(fmt.Sprintf("Schedule: %s – %s (%s)",
			d.cfg.ActiveSchedule.Start, d.cfg.ActiveSchedule.End, d.cfg.ActiveSchedule.Timezone))
	} else {
		d.scheduleLabel.SetText("Schedule: Always active (24/7)")
	}

	d.nextScanLabel.SetText(fmt.Sprintf("Poll interval: %ds", d.cfg.PollInterval))

	statusCard := widget.NewCard("Scanner Status", "Scheduler activity tracking", container.NewVBox(
		d.scannerStatus,
		d.lastScanLabel,
		d.nextScanLabel,
		d.filesLabel,
		d.scheduleLabel,
	))

	// === Controls ===
	d.toggleBtn = widget.NewButtonWithIcon("Start Scanner", theme.MediaPlayIcon(), func() {
		if d.svc.IsRunning() {
			d.svc.Stop()
			d.toggleBtn.SetText("Start Scanner")
			d.toggleBtn.SetIcon(theme.MediaPlayIcon())
			d.toggleBtn.Importance = widget.HighImportance
			d.scannerStatus.SetText("Scanner: Stopped")
		} else {
			d.svc.Start()
			d.toggleBtn.SetText("Stop Scanner")
			d.toggleBtn.SetIcon(theme.MediaStopIcon())
			d.toggleBtn.Importance = widget.DangerImportance
			d.scannerStatus.SetText("Scanner: Running")
		}
	})
	d.toggleBtn.Importance = widget.HighImportance

	d.scanBtn = widget.NewButtonWithIcon("Scan Now", theme.SearchIcon(), func() {
		d.svc.ScanNow()
		d.eventLabel.SetText("Manual scan triggered...")
	})

	controlsCard := widget.NewCard("Control Panel", "Manual scheduler controls", container.NewVBox(
		container.NewGridWithColumns(2, d.toggleBtn, d.scanBtn),
	))

	// === Upload Progress ===
	d.progressBox = container.NewVBox()
	progressCard := widget.NewCard("Active File Transfers", "Real-time sync progress", d.progressBox)

	// === Event Display ===
	eventCard := widget.NewCard("Recent Activity", "Log snapshot", d.eventLabel)

	// === 2-Column Dashboard Layout ===
	leftCol := container.NewVBox(
		connectionCard,
		statusCard,
		controlsCard,
	)
	rightCol := container.NewVBox(
		progressCard,
		eventCard,
	)

	dashboardGrid := container.New(layout.NewGridLayout(2), leftCol, rightCol)

	content := container.NewVBox(
		header,
		widget.NewSeparator(),
		dashboardGrid,
		layout.NewSpacer(),
	)

	scroll := container.NewVScroll(content)
	scroll.SetMinSize(fyne.NewSize(500, 400))

	d.container = container.NewStack(scroll)
}

func (d *Dashboard) listenEvents() {
	for event := range d.svc.EventsCh {
		switch event.Type {
		case "scan_start":
			d.eventLabel.SetText("⏳ " + event.Message)
		case "scan_end":
			d.eventLabel.SetText("✅ " + event.Message)
			d.lastScanLabel.SetText(fmt.Sprintf("Last scan: %s", time.Now().Format(time.TimeOnly)))
		case "upload_start":
			d.eventLabel.SetText("📤 Uploading: " + event.Message)
		case "upload_done":
			d.filesProcessed++
			d.filesLabel.SetText(fmt.Sprintf("Files processed: %d", d.filesProcessed))
			d.eventLabel.SetText("✅ Uploaded: " + event.Message)
			d.removeProgressBar(event.Message)
		case "upload_fail":
			d.eventLabel.SetText("❌ Failed: " + event.Message)
			d.removeProgressBar(event.Message)
		case "status":
			d.eventLabel.SetText("ℹ️ " + event.Message)
		case "error":
			d.eventLabel.SetText("⚠️ " + event.Message)
		}
	}
}

func (d *Dashboard) listenStats() {
	for stat := range d.svc.StatsCh {
		bar, exists := d.progressBars[stat.Filename]
		if !exists {
			bar = widget.NewProgressBar()
			bar.TextFormatter = func() string {
				return fmt.Sprintf("%s — %s", stat.Filename, stat.Speed)
			}
			d.progressBars[stat.Filename] = bar
			d.progressBox.Add(container.NewVBox(
				widget.NewLabel("📄 "+stat.Filename),
				bar,
			))
		}
		bar.SetValue(stat.Percent)
		bar.TextFormatter = func() string {
			return fmt.Sprintf("%.1f%% — %s", stat.Percent*100, stat.Speed)
		}
		bar.Refresh()
	}
}

func (d *Dashboard) removeProgressBar(filename string) {
	if _, exists := d.progressBars[filename]; exists {
		delete(d.progressBars, filename)
		d.progressBox.RemoveAll()
		for fn, bar := range d.progressBars {
			d.progressBox.Add(container.NewVBox(
				widget.NewLabel("📄 "+fn),
				bar,
			))
		}
	}
}

func (d *Dashboard) updateLoop() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if d.svc.SourceConnected() {
			d.srcStatus.FillColor = ColorGreen
			d.srcLabel.SetText(fmt.Sprintf("Source: %s ● Connected", d.cfg.SourceSFTP.Host))
		} else {
			d.srcStatus.FillColor = ColorRed
			d.srcLabel.SetText(fmt.Sprintf("Source: %s ● Disconnected", d.cfg.SourceSFTP.Host))
		}
		d.srcStatus.Refresh()

		if d.cfg.TargetSFTP.Host == "" {
			d.dstStatus.FillColor = ColorYellow
			d.dstLabel.SetText("Destination: Backup-only mode")
		} else if d.svc.DestConnected() {
			d.dstStatus.FillColor = ColorGreen
			d.dstLabel.SetText(fmt.Sprintf("Destination: %s ● Connected", d.cfg.TargetSFTP.Host))
		} else {
			d.dstStatus.FillColor = ColorRed
			d.dstLabel.SetText(fmt.Sprintf("Destination: %s ● Disconnected", d.cfg.TargetSFTP.Host))
		}
		d.dstStatus.Refresh()

		if d.svc.IsRunning() {
			d.scannerStatus.SetText("Scanner: Running")
			d.toggleBtn.SetText("Stop Scanner")
			d.toggleBtn.SetIcon(theme.MediaStopIcon())
			d.toggleBtn.Importance = widget.DangerImportance
		} else {
			d.scannerStatus.SetText("Scanner: Stopped")
			d.toggleBtn.SetText("Start Scanner")
			d.toggleBtn.SetIcon(theme.MediaPlayIcon())
			d.toggleBtn.Importance = widget.HighImportance
		}
	}
}

