#!/bin/sh
# integration-needed.sh — decide whether the VM integration suites run.
# Writes run=true|false to $GITHUB_OUTPUT (stdout when unset) and says why.
#
#   EVENT=<github.event_name> LABELS=<space-separated PR labels> \
#     dev/ci/integration-needed.sh [BASE HEAD]
#
# Always for schedule and workflow_dispatch. For pull requests: when the
# `run-integration` label is set, or when the diff touches anything the suites
# exercise or depend on. static/ is not in the list: the suites drive the API,
# never the UI, and ci-frontend checks the frontend on every PR.
set -eu

out=${GITHUB_OUTPUT:-/dev/stdout}
decide() { echo "run=$1" >>"$out"; echo "integration suites: run=$1 ($2)"; exit 0; }

[ "${EVENT:-}" = pull_request ] || decide true "event ${EVENT:-unknown}"

case " ${LABELS:-} " in
  *" run-integration "*) decide true "run-integration label" ;;
esac

[ $# -eq 2 ] || { echo "usage: $0 BASE HEAD" >&2; exit 2; }
hits=$(git diff --name-only "$1" "$2" | grep -E \
  '^(main\.go|go\.(mod|sum)|Makefile|internal/|playbooks/|tests/integration/|dev/lima-|dev/ci/|\.github/workflows/integration-tests\.yml)' \
  || true)
if [ -n "$hits" ]; then
  echo "$hits"
  decide true "backend paths changed"
fi
decide false "no backend paths changed"
