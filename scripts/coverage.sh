#!/usr/bin/env bash
#
# Run the test suite with coverage and enforce a PER-PACKAGE floor: every
# package must independently meet the threshold, not just the average
# across the repository. Every failing package is listed, and a package
# with real (non-excluded) code but no test files fails outright rather
# than being silently absent from the coverage profile.
#
# Files matching a pattern in coverage-exclude.txt (one glob per line, "#"
# comments) count toward neither numerator nor denominator. The global
# total is still printed, for information only — it is no longer the gate.
#
# Usage: scripts/coverage.sh [threshold]   (threshold defaults to 0)
#
# The threshold is deliberately configurable so it can be ratcheted upward
# as packages are brought up to standard (see issue #6), rather than
# jumping straight to the target and leaving the gate red on main.
set -euo pipefail

threshold="${1:-0}"
profile="coverage.out"
exclusions="coverage-exclude.txt"

go test -covermode=atomic -coverprofile="$profile" ./...

go run ./scripts/coveragegate "$profile" "$threshold" "$exclusions"
