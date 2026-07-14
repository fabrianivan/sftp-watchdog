#!/bin/bash
set -e

# ==========================================================
#  🚀 SFTP Watchdog macOS App & DMG Builder
#  - Installs the Fyne tool locally if missing
#  - Generates standard macOS application bundle (.app)
#  - Automatically packages .app into a drag-and-drop .dmg
# ==========================================================

APP_NAME="SFTP Watchdog"
DMG_NAME="SFTP_Watchdog.dmg"
OUTPUT_DIR="dist"

echo "==============================================="
echo "[*] Building and Packaging SFTP Watchdog for macOS"
echo "==============================================="
echo ""

# Ensure assets contain the correct icon
if [ ! -f "assets/logo.png" ]; then
    echo "[!] Error: assets/logo.png not found. Please provide an icon."
    exit 1
fi

# Ensure Fyne CLI tool is installed
if ! command -v /Users/fabrianivan/go/bin/fyne &>/dev/null; then
    echo "[*] Fyne CLI tool not found. Installing..."
    go install fyne.io/fyne/v2/cmd/fyne@latest
fi

# Clean up previous builds
echo "[*] Cleaning up old build files..."
rm -rf "${APP_NAME}.app"
rm -f "${OUTPUT_DIR}/${DMG_NAME}"
rm -rf "${OUTPUT_DIR}/dmg_root"

# Run Fyne package tool
echo "[*] Packaging application bundle (${APP_NAME}.app)..."
/Users/fabrianivan/go/bin/fyne package -os darwin -icon assets/logo.png -name "${APP_NAME}"

# Setup DMG root
echo "[*] Setting up installer directory for DMG..."
mkdir -p "${OUTPUT_DIR}/dmg_root"
cp -R "${APP_NAME}.app" "${OUTPUT_DIR}/dmg_root/"

# Create symlink to Applications folder
ln -s /Applications "${OUTPUT_DIR}/dmg_root/Applications"

# Create DMG disk image
echo "[*] Packaging app bundle into ${DMG_NAME}..."
hdiutil create -volname "${APP_NAME}" -srcfolder "${OUTPUT_DIR}/dmg_root" -ov -format UDZO "${OUTPUT_DIR}/${DMG_NAME}"

# Clean up temporary directories
rm -rf "${OUTPUT_DIR}/dmg_root"

echo ""
echo "==============================================="
echo "✅ Build and Packaging completed successfully!"
echo "App bundle: ./${APP_NAME}.app"
echo "DMG Installer: ./${OUTPUT_DIR}/${DMG_NAME}"
echo "==============================================="
echo ""
