#!/usr/bin/env bash
#
# Run the Swift test suite with coverage enabled, leaving the profile data for
# `scripts/coverage.sh` to read (see the `coverage` mise task).
#
# swift-testing's Testing.framework ships with a full Xcode, not with the
# Command Line Tools. A machine with both installed but `xcode-select` pointed
# at the CLT fails with "no such module 'Testing'" — the tests are fine, the
# toolchain just cannot see the framework. Rather than require every developer
# to `sudo xcode-select -s`, fall back to a full Xcode when the selected
# developer directory has no Swift Testing support. CI runners already select a
# full Xcode, so this is a no-op there.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/../macos"

# Relative to a developer directory, the macOS Testing.framework lives here.
testing_framework="Platforms/MacOSX.platform/Developer/Library/Frameworks/Testing.framework"

developer_dir="$(xcode-select -p 2>/dev/null || true)"
if [ ! -d "$developer_dir/$testing_framework" ]; then
  for candidate in /Applications/Xcode*.app/Contents/Developer; do
    if [ -d "$candidate/$testing_framework" ]; then
      echo "note: ${developer_dir:-no developer directory} has no Testing.framework; using $candidate" >&2
      export DEVELOPER_DIR="$candidate"
      break
    fi
  done
fi

exec swift test --enable-code-coverage
