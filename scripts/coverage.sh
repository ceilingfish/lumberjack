#!/usr/bin/env bash
#
# Run the test suite with coverage, report it per file (lowest first), and
# fail if total statement coverage falls below the threshold.
#
# Usage: scripts/coverage.sh [threshold]   (threshold defaults to 80)
set -euo pipefail

threshold="${1:-80}"
profile="coverage.out"

go test -covermode=atomic -coverprofile="$profile" ./...

awk -v threshold="$threshold" '
/^mode:/ { next }
{
  file = $1
  sub(/:[0-9]+\.[0-9]+,[0-9]+\.[0-9]+$/, "", file)
  tot[file] += $2
  gtot += $2
  if ($3 > 0) { cov[file] += $2; gcov += $2 }
}
END {
  for (f in tot)
    printf "%6.1f%%  %s\n", (tot[f] ? 100 * cov[f] / tot[f] : 0), f | "sort -n"
  close("sort -n")
  g = gtot ? 100 * gcov / gtot : 0
  printf "-------\n%6.1f%%  TOTAL\n", g
  if (g < threshold) {
    printf "FAIL: total coverage %.1f%% is below the %d%% threshold\n", g, threshold > "/dev/stderr"
    exit 1
  }
}
' "$profile"
