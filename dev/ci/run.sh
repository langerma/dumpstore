#!/bin/sh
# run.sh — run CI make targets in parallel and print a pass/fail table.
# Used by `make ci`; each job's output goes to .cache/ci/<job>.log.
#
#   dev/ci/run.sh ci-go ci-frontend ...
set -u

LOGS=.cache/ci
mkdir -p "$LOGS"
rm -f "$LOGS"/*.result
MAKE=${MAKE:-make}

# Same Node major as CI (.node-version), or results may differ.
if command -v node >/dev/null 2>&1; then
  want=$(cut -d. -f1 .node-version)
  have=$(node --version | sed 's/^v//; s/\..*//')
  [ "$want" = "$have" ] || echo "warning: node $have here, CI uses node $want (.node-version)" >&2
fi

start=$(date +%s)
for job in "$@"; do
  ( s=$(date +%s)
    if $MAKE --no-print-directory "$job" >"$LOGS/$job.log" 2>&1; then r=pass; else r=FAIL; fi
    echo "$r $(( $(date +%s) - s ))s" >"$LOGS/$job.result" ) &
done
wait

failed=0
echo ""
for job in "$@"; do
  read -r res secs <"$LOGS/$job.result"
  printf '%-18s %-5s %6s   %s\n' "$job" "$res" "$secs" "$LOGS/$job.log"
  [ "$res" = pass ] || failed=1
done
echo "total $(( $(date +%s) - start ))s"

if [ "$failed" = 1 ]; then
  echo ""
  for job in "$@"; do
    grep -q '^FAIL' "$LOGS/$job.result" || continue
    echo "==== $job (last 25 lines of $LOGS/$job.log)"
    tail -n 25 "$LOGS/$job.log"
  done
  exit 1
fi
