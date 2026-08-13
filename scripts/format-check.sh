#!/usr/bin/env bash
#
# Check Go formatting without writing anything, unlike the `format` task
# (`gofumpt -w .`). The release workflow needs a check-only variant so a
# misformatted `main` blocks the release instead of being silently reformatted
# in place (see AGENTS.md: the release path never commits a fix).
#
# Usage: scripts/format-check.sh
set -euo pipefail

diff="$(gofumpt -l .)"
if [ -n "$diff" ]; then
  echo "The following files are not gofumpt-formatted:" >&2
  echo "$diff" >&2
  exit 1
fi
