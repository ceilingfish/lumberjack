#!/usr/bin/env bash
# Builds the Swift package in release mode and assembles it into
# LumberjackMenuBar.app — a proper macOS app bundle with an Info.plist
# (LSUIElement, so it's a menu-bar-only app with no Dock icon), ad-hoc
# code-signed so Gatekeeper and the notification/UDS entitlements it needs
# work on the machine that built it.
#
# Usage: macos/scripts/build-app.sh [output-dir]
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

OUT_DIR="${1:-.build/app}"
APP="$OUT_DIR/LumberjackMenuBar.app"

swift build -c release

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS"
cp "$(swift build -c release --show-bin-path)/LumberjackMenuBar" "$APP/Contents/MacOS/LumberjackMenuBar"
cp Resources/Info.plist "$APP/Contents/Info.plist"

codesign --force --deep --sign - "$APP"

echo "Built $APP"
