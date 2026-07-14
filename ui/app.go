package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"

	"SFTPUpload/internal/config"
	"SFTPUpload/internal/logging"
	"SFTPUpload/internal/service"
)

// App is the main application window with tabbed interface and system tray.
type App struct {
	fyneApp fyne.App
	window  fyne.Window
	svc     *service.Service
	cfg     *config.Config
	cfgPath string
	logger  *logging.Logger
	version string

	dashboard    *Dashboard
	configEditor *ConfigEditor
	logViewer    *LogViewer
}

// NewApp creates the main SFTP Watchdog application window.
func NewApp(fyneApp fyne.App, svc *service.Service, cfg *config.Config, cfgPath string, logger *logging.Logger, version string) *App {
	a := &App{
		fyneApp: fyneApp,
		svc:     svc,
		cfg:     cfg,
		cfgPath: cfgPath,
		logger:  logger,
		version: version,
	}

	a.window = fyneApp.NewWindow("SFTP Watchdog v" + version)
	a.window.Resize(fyne.NewSize(720, 560))
	a.window.SetMaster()

	// Close to tray instead of quitting
	a.window.SetCloseIntercept(func() {
		a.window.Hide()
	})

	a.buildTabs()
	a.setupSystemTray()

	return a
}

func (a *App) buildTabs() {
	// Create tab components
	a.dashboard = NewDashboard(a.svc, a.cfg)
	a.configEditor = NewConfigEditor(a.cfg, a.cfgPath, a.window, func(newCfg *config.Config) {
		a.logger.Write("Configuration updated from GUI")
	})
	a.logViewer = NewLogViewer(a.logger, a.cfg.LogFile)

	tabs := container.NewAppTabs(
		container.NewTabItemWithIcon("Dashboard", theme.HomeIcon(), a.dashboard.Container()),
		container.NewTabItemWithIcon("Configuration", theme.SettingsIcon(), a.configEditor.Container()),
		container.NewTabItemWithIcon("Logs", theme.ListIcon(), a.logViewer.Container()),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	a.window.SetContent(tabs)
}

func (a *App) setupSystemTray() {
	if desk, ok := a.fyneApp.(desktop.App); ok {
		menu := fyne.NewMenu("SFTP Watchdog",
			fyne.NewMenuItem("Show Window", func() {
				a.window.Show()
				a.window.RequestFocus()
			}),
			fyne.NewMenuItem("Scan Now", func() {
				a.svc.ScanNow()
				a.logger.Write("Scan triggered from system tray")
			}),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("Quit", func() {
				a.svc.Stop()
				a.svc.CloseAll()
				a.fyneApp.Quit()
			}),
		)
		desk.SetSystemTrayMenu(menu)
	}
}

// Show displays the main window.
func (a *App) Show() {
	a.window.Show()
}

// Window returns the underlying fyne.Window.
func (a *App) Window() fyne.Window {
	return a.window
}

// SendNotification sends an OS notification via Fyne.
func (a *App) SendNotification(title, content string) {
	a.fyneApp.SendNotification(fyne.NewNotification(title, content))
}
