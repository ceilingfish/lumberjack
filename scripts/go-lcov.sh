#!/usr/bin/env bash
#
# Convert the Go coverage profile from `cli:test` into lcov, for merging with
# the Swift half (scripts/osx-lcov.sh) in scripts/coverage.sh.
#
# The Go profile counts statements per block, lcov counts lines: each block's
# count is applied to every line it spans, highest wins where blocks overlap.
# Import paths are rewritten repo-relative to match the Swift half. Sorting goes
# through sort(1) because awk's asorti() is absent from the BSD awk on macOS.
#
# Usage: scripts/go-lcov.sh [profile] [output]   # coverage.out -> coverage-go.lcov
set -euo pipefail

profile="${1:-coverage.out}"
output="${2:-coverage-go.lcov}"

if [ ! -f "$profile" ]; then
  echo "error: $profile not found — run 'mise run cli:test' first" >&2
  exit 1
fi

module="$(awk '/^module /{ print $2; exit }' go.mod)"

awk -v module="$module" -v OFS='\t' '
/^mode:/    { next }
/\.pb\.go:/ { next }   # generated protobuf/gRPC stubs
{
  # "<path>:<startLine>.<col>,<endLine>.<col> <numStmt> <count>"
  split($1, loc, ":")
  file = loc[1]
  sub("^" module "/", "", file)

  split(loc[2], span, ",")
  split(span[1], from, ".")
  split(span[2], to, ".")

  for (line = from[1]; line <= to[1]; line++) print file, line, $3
}
' "$profile" \
  | sort -t"$(printf '\t')" -k1,1 -k2,2n \
  | awk -F'\t' '
function flush_line() {
  if (line == "") return
  printf "DA:%s,%d\n", line, best
  found++
  if (best > 0) hit++
}
function flush_file() {
  if (file == "") return
  flush_line()
  printf "LF:%d\nLH:%d\nend_of_record\n", found, hit
}
{
  if ($1 != file) {
    flush_file()
    file = $1; line = ""; found = 0; hit = 0
    printf "SF:%s\n", file
  }
  if ($2 != line) { flush_line(); line = $2; best = $3 }
  else if ($3 > best) { best = $3 }
}
END { flush_file() }
' > "$output"

echo "wrote $output ($(grep -c '^SF:' "$output") files)"
