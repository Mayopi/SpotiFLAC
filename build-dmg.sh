#!/bin/bash
set -e

export PATH="$HOME/go/bin:$PATH"

APP_NAME="SpotiFLAC"
VERSION=$(grep -o '"productVersion":\s*"[^"]*"' wails.json | head -1 | cut -d'"' -f4)
DMG_NAME="${APP_NAME}-${VERSION}-macOS"
BUILD_DIR="build/bin"
DMG_DIR="build/dmg"
APP_BUNDLE="${BUILD_DIR}/${APP_NAME}.app"

# Detect architecture
ARCH=$(uname -m)
if [ "$ARCH" = "arm64" ]; then
    PLATFORM="darwin/arm64"
    DMG_NAME="${DMG_NAME}-arm64"
else
    PLATFORM="darwin/amd64"
    DMG_NAME="${DMG_NAME}-amd64"
fi

echo "==> Building ${APP_NAME} v${VERSION} for ${PLATFORM}..."
wails build -platform "$PLATFORM" -clean

if [ ! -d "$APP_BUNDLE" ]; then
    echo "ERROR: Build failed — ${APP_BUNDLE} not found."
    exit 1
fi

echo "==> Creating DMG..."

# Clean up previous DMG staging
rm -rf "$DMG_DIR"
mkdir -p "$DMG_DIR"

# Copy .app bundle into staging directory
cp -R "$APP_BUNDLE" "$DMG_DIR/"

# Create a symlink to /Applications for drag-and-drop install
ln -s /Applications "$DMG_DIR/Applications"

# Create the DMG
rm -f "${BUILD_DIR}/${DMG_NAME}.dmg"
hdiutil create \
    -volname "$APP_NAME" \
    -srcfolder "$DMG_DIR" \
    -ov \
    -format UDZO \
    "${BUILD_DIR}/${DMG_NAME}.dmg"

# Clean up staging directory
rm -rf "$DMG_DIR"

echo "==> Done! DMG created at: ${BUILD_DIR}/${DMG_NAME}.dmg"
