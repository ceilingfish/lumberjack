#!/usr/bin/env bash
#
# Export the Swift coverage from `osx:test` as lcov, the counterpart to
# scripts/go-lcov.sh. macOS-only: llvm-cov comes from the Xcode toolchain.
#
# Excludes .build/ (dependency checkouts and SwiftPM's derived runner.swift),
# Tests/ and Generated/, leaving the 8 files under Sources/LumberjackMenuBar.
# The LumberjackMenuBar.json llvm already wrote is unused — it spans all 600-odd
# files in the build, so its own totals are meaningless.
#
# Usage: scripts/osx-lcov.sh [output]        # default: coverage-swift.lcov
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
output="${1:-$repo_root/coverage-swift.lcov}"

cd "$repo_root/macos"

profdata=".build/debug/codecov/default.profdata"
binary=".build/debug/LumberjackMenuBarPackageTests.xctest/Contents/MacOS/LumberjackMenuBarPackageTests"

for required in "$profdata" "$binary"; do
  if [ ! -e "$required" ]; then
    echo "error: $required not found — run 'mise run osx:test' first" >&2
    exit 1
  fi
done

# sed rewrites llvm-cov's absolute paths repo-relative, to match the Go half.
xcrun llvm-cov export \
  -format=lcov \
  -instr-profile "$profdata" \
  "$binary" \
  --ignore-filename-regex='(\.build|Tests|Generated)/' \
  | sed "s|^SF:$repo_root/|SF:|" > "$output"

echo "wrote $output ($(grep -c '^SF:' "$output") files)"
