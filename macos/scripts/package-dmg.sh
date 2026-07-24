#!/usr/bin/env bash
# Packages the built .app (see build-app.sh) into a .dmg for release, the
# distribution vehicle for this app per issue #9 — separate from, and never
# bundled into, the Go CLI's install command or release artifacts.
#
# Usage: macos/scripts/package-dmg.sh [app-dir] [output-dmg]
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

APP_DIR="${1:-.build/app}"
APP="$APP_DIR/LumberjackMenuBar.app"
OUT="${2:-.build/LumberjackMenuBar.dmg}"

if [ ! -d "$APP" ]; then
  echo "error: $APP not found — run build-app.sh first" >&2
  exit 1
fi

rm -f "$OUT"
hdiutil create -volname "Lumberjack" -srcfolder "$APP" -ov -format UDZO "$OUT"

echo "Built $OUT"
