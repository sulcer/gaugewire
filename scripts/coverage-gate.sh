#!/bin/sh
# Hard coverage gate for the pure packages. Everything else is reported, not gated.
# A package that does not exist yet is skipped, so the gate works from the first commit.
set -eu

minimum=90
status=0
for pkg in ./internal/quota ./internal/source/claude; do
  if [ ! -d "$pkg" ]; then
    echo "coverage-gate: $pkg not present, skipped"
    continue
  fi
  line=$(go test -cover -count=1 "$pkg" | grep -o 'coverage: [0-9.]*%' || true)
  pct=${line#coverage: }
  pct=${pct%\%}
  echo "coverage-gate: $pkg ${pct:-0}%"
  if awk -v p="${pct:-0}" -v m="$minimum" 'BEGIN { exit !(p < m) }'; then
    echo "coverage-gate: $pkg is below ${minimum}%" >&2
    status=1
  fi
done
exit $status
