package ui

import (
	"fmt"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"SFTPUpload/internal/config"
)

// ConfigEditor provides a form-based configuration editor tab.
type ConfigEditor struct {
	container *fyne.Container
	cfg       *config.Config
	cfgPath   string
	window    fyne.Window
	onSave    func(*config.Config)

	// Source SFTP fields
	srcHost      *widget.Entry
	srcPort      *widget.Entry
	srcUser      *widget.Entry
	srcPass      *widget.Entry
	srcDir       *widget.Entry
	srcFinger    *widget.Entry

	// Target SFTP fields
	dstHost      *widget.Entry
	dstPort      *widget.Entry
	dstUser      *widget.Entry
	dstPass      *widget.Entry
	dstDir       *widget.Entry
	dstFinger    *widget.Entry

	// General fields
	backupDir    *widget.Entry
	pollInterval *widget.Entry
	idleTimeout  *widget.Entry
	reconnectInt *widget.Entry
	reconnectRet *widget.Entry
	keepAlive    *widget.Entry
	maxIdleScans *widget.Entry
	logFile      *widget.Entry
	logRetention *widget.Entry

	// Schedule fields
	schedEnabled *widget.Check
	schedStart   *widget.Entry
	schedEnd     *widget.Entry
	schedTZ      *widget.Entry

	// Initial sync
	initialSync  *widget.Check
}

// NewConfigEditor creates the configuration editor tab.
func NewConfigEditor(cfg *config.Config, cfgPath string, window fyne.Window, onSave func(*config.Config)) *ConfigEditor {
	ce := &ConfigEditor{
		cfg:     cfg,
		cfgPath: cfgPath,
		window:  window,
		onSave:  onSave,
	}

	ce.buildFields()
	ce.populateFields()

	// Source SFTP section
	srcForm := widget.NewForm(
		widget.NewFormItem("Host", ce.srcHost),
		widget.NewFormItem("Port", ce.srcPort),
		widget.NewFormItem("Username", ce.srcUser),
		widget.NewFormItem("Password", ce.srcPass),
		widget.NewFormItem("Directory", ce.srcDir),
		widget.NewFormItem("Fingerprint", ce.srcFinger),
	)
	srcCard := widget.NewCard("Source SFTP", "Connection to watch for files", srcForm)

	// Target SFTP section
	dstForm := widget.NewForm(
		widget.NewFormItem("Host", ce.dstHost),
		widget.NewFormItem("Port", ce.dstPort),
		widget.NewFormItem("Username", ce.dstUser),
		widget.NewFormItem("Password", ce.dstPass),
		widget.NewFormItem("Directory", ce.dstDir),
		widget.NewFormItem("Fingerprint", ce.dstFinger),
	)
	dstCard := widget.NewCard("Target SFTP", "Destination to upload files (leave host empty for backup-only)", dstForm)

	// General settings section
	generalForm := widget.NewForm(
		widget.NewFormItem("Backup Directory", ce.backupDir),
		widget.NewFormItem("Poll Interval (sec)", ce.pollInterval),
		widget.NewFormItem("Idle Timeout (sec)", ce.idleTimeout),
		widget.NewFormItem("Reconnect Interval (sec)", ce.reconnectInt),
		widget.NewFormItem("Reconnect Retries", ce.reconnectRet),
		widget.NewFormItem("Keep-Alive (sec)", ce.keepAlive),
		widget.NewFormItem("Max Idle Scans", ce.maxIdleScans),
		widget.NewFormItem("Log File", ce.logFile),
		widget.NewFormItem("Log Retention (days)", ce.logRetention),
		widget.NewFormItem("Initial Sync on Start", ce.initialSync),
	)
	generalCard := widget.NewCard("General Settings", "", generalForm)

	// Schedule section
	schedForm := widget.NewForm(
		widget.NewFormItem("Enabled", ce.schedEnabled),
		widget.NewFormItem("Start Time (HH:MM)", ce.schedStart),
		widget.NewFormItem("End Time (HH:MM)", ce.schedEnd),
		widget.NewFormItem("Timezone", ce.schedTZ),
	)
	schedCard := widget.NewCard("Active Schedule", "When scanning is active (disable for 24/7)", schedForm)

	// Save button
	saveBtn := widget.NewButton("💾  Save Configuration", func() {
		ce.save()
	})
	saveBtn.Importance = widget.HighImportance

	// Build scrollable layout
	content := container.NewVBox(
		srcCard,
		dstCard,
		generalCard,
		schedCard,
		layout.NewSpacer(),
		container.NewHBox(layout.NewSpacer(), saveBtn, layout.NewSpacer()),
	)

	scroll := container.NewVScroll(content)
	scroll.SetMinSize(fyne.NewSize(500, 400))

	ce.container = container.NewStack(scroll)
	return ce
}

// Container returns the Fyne container for this tab.
func (ce *ConfigEditor) Container() *fyne.Container {
	return ce.container
}

func (ce *ConfigEditor) buildFields() {
	ce.srcHost = widget.NewEntry()
	ce.srcPort = widget.NewEntry()
	ce.srcUser = widget.NewEntry()
	ce.srcPass = widget.NewPasswordEntry()
	ce.srcDir = widget.NewEntry()
	ce.srcFinger = widget.NewEntry()
	ce.srcFinger.SetPlaceHolder("SHA256:... (optional)")

	ce.dstHost = widget.NewEntry()
	ce.dstHost.SetPlaceHolder("Leave empty for backup-only mode")
	ce.dstPort = widget.NewEntry()
	ce.dstUser = widget.NewEntry()
	ce.dstPass = widget.NewPasswordEntry()
	ce.dstDir = widget.NewEntry()
	ce.dstFinger = widget.NewEntry()
	ce.dstFinger.SetPlaceHolder("SHA256:... (optional)")

	ce.backupDir = widget.NewEntry()
	ce.pollInterval = widget.NewEntry()
	ce.idleTimeout = widget.NewEntry()
	ce.reconnectInt = widget.NewEntry()
	ce.reconnectRet = widget.NewEntry()
	ce.keepAlive = widget.NewEntry()
	ce.maxIdleScans = widget.NewEntry()
	ce.logFile = widget.NewEntry()
	ce.logRetention = widget.NewEntry()

	ce.schedEnabled = widget.NewCheck("", nil)
	ce.schedStart = widget.NewEntry()
	ce.schedEnd = widget.NewEntry()
	ce.schedTZ = widget.NewEntry()
	ce.schedTZ.SetPlaceHolder("Local")

	ce.initialSync = widget.NewCheck("Run initial scan on startup", nil)
}

func (ce *ConfigEditor) populateFields() {
	ce.srcHost.SetText(ce.cfg.SourceSFTP.Host)
	ce.srcPort.SetText(strconv.Itoa(ce.cfg.SourceSFTP.Port))
	ce.srcUser.SetText(ce.cfg.SourceSFTP.Username)
	ce.srcPass.SetText(ce.cfg.SourceSFTP.Password)
	ce.srcDir.SetText(ce.cfg.SourceSFTP.TargetDirectory)
	ce.srcFinger.SetText(ce.cfg.SourceSFTP.ExpectedFingerprint)

	ce.dstHost.SetText(ce.cfg.TargetSFTP.Host)
	ce.dstPort.SetText(strconv.Itoa(ce.cfg.TargetSFTP.Port))
	ce.dstUser.SetText(ce.cfg.TargetSFTP.Username)
	ce.dstPass.SetText(ce.cfg.TargetSFTP.Password)
	ce.dstDir.SetText(ce.cfg.TargetSFTP.TargetDirectory)
	ce.dstFinger.SetText(ce.cfg.TargetSFTP.ExpectedFingerprint)

	ce.backupDir.SetText(ce.cfg.BackupDirectory)
	ce.pollInterval.SetText(strconv.Itoa(ce.cfg.PollInterval))
	ce.idleTimeout.SetText(strconv.Itoa(ce.cfg.IdleTimeoutSeconds))
	ce.reconnectInt.SetText(strconv.Itoa(ce.cfg.ReconnectInterval))
	ce.reconnectRet.SetText(strconv.Itoa(ce.cfg.ReconnectRetries))
	ce.keepAlive.SetText(strconv.Itoa(ce.cfg.KeepAliveDuration))
	ce.maxIdleScans.SetText(strconv.Itoa(ce.cfg.MaxIdleScans))
	ce.logFile.SetText(ce.cfg.LogFile)
	ce.logRetention.SetText(strconv.Itoa(ce.cfg.LogRetentionDays))

	ce.schedEnabled.SetChecked(ce.cfg.ActiveSchedule.Enabled)
	ce.schedStart.SetText(ce.cfg.ActiveSchedule.Start)
	ce.schedEnd.SetText(ce.cfg.ActiveSchedule.End)
	ce.schedTZ.SetText(ce.cfg.ActiveSchedule.Timezone)

	if ce.cfg.EnableInitialSync != nil {
		ce.initialSync.SetChecked(*ce.cfg.EnableInitialSync)
	} else {
		ce.initialSync.SetChecked(true)
	}
}

func (ce *ConfigEditor) save() {
	// Read values from fields
	srcPort, err := strconv.Atoi(ce.srcPort.Text)
	if err != nil {
		dialog.ShowError(fmt.Errorf("invalid source port: %s", ce.srcPort.Text), ce.window)
		return
	}
	dstPort, err := strconv.Atoi(ce.dstPort.Text)
	if err != nil {
		dialog.ShowError(fmt.Errorf("invalid target port: %s", ce.dstPort.Text), ce.window)
		return
	}

	pollInt, _ := strconv.Atoi(ce.pollInterval.Text)
	idleTm, _ := strconv.Atoi(ce.idleTimeout.Text)
	reconInt, _ := strconv.Atoi(ce.reconnectInt.Text)
	reconRet, _ := strconv.Atoi(ce.reconnectRet.Text)
	keepAlv, _ := strconv.Atoi(ce.keepAlive.Text)
	maxIdle, _ := strconv.Atoi(ce.maxIdleScans.Text)
	logRet, _ := strconv.Atoi(ce.logRetention.Text)

	initSync := ce.initialSync.Checked

	newCfg := &config.Config{
		SourceSFTP: config.SFTPConfig{
			Host:                ce.srcHost.Text,
			Port:                srcPort,
			Username:            ce.srcUser.Text,
			Password:            ce.srcPass.Text,
			TargetDirectory:     ce.srcDir.Text,
			ExpectedFingerprint: ce.srcFinger.Text,
		},
		TargetSFTP: config.SFTPConfig{
			Host:                ce.dstHost.Text,
			Port:                dstPort,
			Username:            ce.dstUser.Text,
			Password:            ce.dstPass.Text,
			TargetDirectory:     ce.dstDir.Text,
			ExpectedFingerprint: ce.dstFinger.Text,
		},
		BackupDirectory:    ce.backupDir.Text,
		PollInterval:       pollInt,
		IdleTimeoutSeconds: idleTm,
		ReconnectInterval:  reconInt,
		ReconnectRetries:   reconRet,
		KeepAliveDuration:  keepAlv,
		MaxIdleScans:       maxIdle,
		LogFile:            ce.logFile.Text,
		LogRetentionDays:   logRet,
		ShowBalloonTimeout: ce.cfg.ShowBalloonTimeout,
		ActiveSchedule: config.ScheduleConfig{
			Enabled:  ce.schedEnabled.Checked,
			Start:    ce.schedStart.Text,
			End:      ce.schedEnd.Text,
			Timezone: ce.schedTZ.Text,
		},
		EnableInitialSync: &initSync,
	}

	if err := config.Save(newCfg, ce.cfgPath); err != nil {
		dialog.ShowError(fmt.Errorf("failed to save config: %v", err), ce.window)
		return
	}

	// Update in-memory reference
	*ce.cfg = *newCfg

	dialog.ShowInformation("Configuration Saved", "Config has been saved.\nRestart the scanner for changes to take effect.", ce.window)

	if ce.onSave != nil {
		ce.onSave(newCfg)
	}
}
