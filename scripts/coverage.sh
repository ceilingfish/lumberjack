#!/usr/bin/env bash
#
# Merge the Go and Swift lcov reports, print per file (lowest first) with a
# per-language subtotal, and fail if the *merged* line coverage is below the
# threshold. The two codebases share one budget, so a gap in either fails the
# whole project rather than staying contained to its half.
#
# Runs no tests and reads no raw profiles: scripts/go-lcov.sh and
# scripts/osx-lcov.sh normalise those into the lcov halves this merges. Merging
# is concatenation — lcov SF: sections are self-contained and the halves cover
# disjoint files. coverage.lcov is kept for Codecov and editor gutter plugins.
#
# Usage: scripts/coverage.sh <threshold> <report.lcov>...
#   e.g. scripts/coverage.sh 80 coverage-go.lcov coverage-swift.lcov
set -euo pipefail

threshold="${1:-80}"
shift || true

reports=("$@")
if [ "${#reports[@]}" -eq 0 ]; then
  reports=(coverage-go.lcov coverage-swift.lcov)
fi

for report in "${reports[@]}"; do
  if [ ! -f "$report" ]; then
    echo "error: $report not found — run 'mise run coverage' to build both halves" >&2
    exit 1
  fi
done

merged="coverage.lcov"
cat "${reports[@]}" > "$merged"

awk -v threshold="$threshold" '
/^SF:/ { file = substr($0, 4); next }
/^DA:/ {
  split(substr($0, 4), da, ",")
  tot[file]++
  gtot++
  half = (file ~ /^macos\//) ? "Swift" : "Go"
  htot[half]++
  if (da[2] > 0) { cov[file]++; gcov++; hcov[half]++ }
  next
}
END {
  for (f in tot)
    printf "%6.1f%%  %s\n", (tot[f] ? 100 * cov[f] / tot[f] : 0), f | "sort -n"
  close("sort -n")
  printf "-------\n"
  for (h in htot)
    printf "%6.1f%%  %s subtotal (%d/%d lines)\n", \
      (htot[h] ? 100 * hcov[h] / htot[h] : 0), h, hcov[h], htot[h]
  g = gtot ? 100 * gcov / gtot : 0
  printf "%6.1f%%  MERGED TOTAL (%d/%d lines)\n", g, gcov, gtot
  if (g < threshold) {
    printf "FAIL: merged coverage %.1f%% is below the %d%% threshold\n", g, threshold > "/dev/stderr"
    exit 1
  }
}
' "$merged"
