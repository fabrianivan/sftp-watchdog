# SFTP Watchdog

Cross-platform SFTP file mover with a GUI: watch a source SFTP folder, copy new files to a target SFTP, then move the originals to a dated backup directory. Includes a desktop GUI with system tray, scheduling, reconnect/keepalive, and daily log rotation.

## Features
- **Desktop GUI** — tabbed interface with Dashboard, Configuration, and Logs tabs
- **System tray** — minimizes to tray, keeps monitoring in background
- **Cross-platform** — runs on Windows and macOS (Intel + Apple Silicon)
- Scheduled or manual scans; GUI controls for Start/Stop and Scan Now
- Hash-based dedupe via `uploaded.json` to skip already-processed files
- Automatic reconnect/keepalive with idle disconnects to save resources
- Date-based backup subfolders on the source SFTP after successful uploads
- Daily log rotation with retention cleanup
- Real-time log viewer in the GUI

## Configuration
Create `config.json` beside the binary (or pass `-config path`):
```json
{
  "sourceSFTP": {
    "host": "source.example.com",
    "port": 22,
    "username": "user",
    "password": "pass",
    "targetDirectory": "/source/upload",
    "expectedFingerprint": "SHA256:..."
  },
  "targetSFTP": {
    "host": "target.example.com",
    "port": 22,
    "username": "user",
    "password": "pass",
    "targetDirectory": "/target/upload",
    "expectedFingerprint": "SHA256:..."
  },
  "backupDirectory": "/source/backup",
  "idleTimeoutSeconds": 300,
  "reconnectInterval": 10,
  "reconnectRetries": 5,
  "logFile": "sftp_uploader.log",
  "logRetentionDays": 7,
  "showBalloonTimeout": 10,
  "pollInterval": 30,
  "activeSchedule": {
    "enabled": true,
    "start": "05:00",
    "end": "23:45",
    "timezone": "Local"
  },
  "keepAliveDuration": 90,
  "enableInitialSync": true,
  "maxIdleScans": 10
}
```

Key notes:
- `activeSchedule.enabled` false means always-on scanning.
- If `targetSFTP.host` is empty, files are only moved to backup.
- `uploaded.json` is created automatically to track processed hashes.
- Configuration can also be edited from the GUI's Configuration tab.

## Usage

### GUI Mode (default)
```bash
# macOS (Apple Silicon)
./sftpwatchdog-macos-arm64

# macOS (Intel)
./sftpwatchdog-macos-amd64

# Windows
sftpwatchdog.exe
```

The app opens a windowed GUI with three tabs:
1. **Dashboard** — connection status, scanner controls, upload progress
2. **Configuration** — edit all settings via form fields
3. **Logs** — real-time scrolling log viewer

Closing the window minimizes to system tray. Use the tray menu to show/hide the window or quit.

### Headless Mode (single scan)
```bash
# Run one scan and exit (no GUI)
sftpwatchdog --scan
```

### Build for Windows
```bat
REM Requires Go 1.22+ and a C compiler (e.g., MinGW-w64)
SET CGO_ENABLED=1
SET GOOS=windows
SET GOARCH=amd64
go build -ldflags "-H=windowsgui -s -w" -o sftpwatchdog.exe .
```

Or use the build script:
```bat
build.bat
```

### Build for macOS
```bash
# Apple Silicon
CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -o sftpwatchdog-macos-arm64

# Intel Mac
CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build -o sftpwatchdog-macos-amd64
```

Or use the build script:
```bash
./build.sh
```

## GitHub Actions (auto build + release)
- Every push/PR runs `go test` and `go build` on Windows and macOS.
- Tagging a release (e.g., `v1.2.3`) creates a GitHub Release and attaches:
  - `sftpwatchdog.exe` (Windows)
  - `sftpwatchdog-macos-arm64` (macOS Apple Silicon)
  - `sftpwatchdog-macos-amd64` (macOS Intel)

## Development
- Go 1.22+
- Fyne v2 for the GUI (requires CGO on macOS, optional on Windows)
- Run `gofmt ./...` before committing.
- The app stores logs with daily rotation and keeps at most `logRetentionDays`.

## Architecture
```
main.go               → App entry point (GUI or headless)
ui/
├── app.go            → Main window, tabs, system tray
├── dashboard.go      → Status indicators, scanner controls
├── config_editor.go  → Form-based config editor
├── log_viewer.go     → Real-time log viewer
└── theme.go          → Custom dark theme
internal/
├── config/           → JSON config loader/saver
├── logging/          → Daily-rotating logger with subscriber support
├── notifier/         → Notification interface
├── service/          → Scan/upload/backup orchestrator
├── sftpclient/       → SSH/SFTP connection manager
└── uploaded/         → Hash-based dedup tracker
```
