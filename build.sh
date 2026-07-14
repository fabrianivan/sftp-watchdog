#!/bin/bash
set -e

# ==========================================================
#  🚀 SFTP Watchdog macOS Build Script
#  - Builds for arm64 (Apple Silicon) and amd64 (Intel)
#  - Uses CGO for Fyne (required on macOS)
# ==========================================================

APP_NAME="sftpwatchdog"
OUTPUT_DIR="dist"
VERSION="${1:-dev}"

echo ""
echo "==============================================="
echo "[*] Building SFTP Watchdog v${VERSION} for macOS"
echo "==============================================="
echo ""

# Ensure output directory exists
mkdir -p "${OUTPUT_DIR}"

# Ensure macOS SDK is available
if command -v xcrun &>/dev/null; then
    export SDKROOT=$(xcrun --sdk macosx --show-sdk-path)
    echo "[*] SDK: ${SDKROOT}"
fi

LDFLAGS="-s -w -X main.version=${VERSION}"

# Build for Apple Silicon (arm64)
echo "[*] Building for arm64 (Apple Silicon)..."
CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
    go build -ldflags "${LDFLAGS}" -o "${OUTPUT_DIR}/${APP_NAME}-macos-arm64"
echo "[✓] arm64 build complete"

# Build for Intel (amd64) — cross-compile from arm64
echo "[*] Building for amd64 (Intel Macs)..."
if command -v xcrun &>/dev/null; then
    CGO_ENABLED=1 \
    CC="$(xcrun --sdk macosx --find clang)" \
    CXX="$(xcrun --sdk macosx --find clang++)" \
    CGO_CFLAGS="-arch x86_64" \
    CGO_LDFLAGS="-arch x86_64" \
    GOOS=darwin GOARCH=amd64 \
    go build -ldflags "${LDFLAGS}" -o "${OUTPUT_DIR}/${APP_NAME}-macos-amd64"
    echo "[✓] amd64 build complete"
else
    echo "[!] xcrun not found — skipping amd64 cross-compile"
fi

echo ""
echo "==============================================="
echo "✅ Build successful!"
echo "Output: ${OUTPUT_DIR}/"
ls -la "${OUTPUT_DIR}/${APP_NAME}-macos-"*
echo "==============================================="
echo ""
