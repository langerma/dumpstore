#!/bin/sh
# check-docs.sh — fail when code changed but README.md / docs/index.html did not.
# Single source for the check-docs workflow and `make ci-docs`.
#
#   dev/ci/check-docs.sh BASE HEAD   compare two commits (CI)
#   dev/ci/check-docs.sh [BASE]      compare BASE with the working tree, untracked
#                                    files included (local; BASE defaults to the
#                                    merge base with origin/main)
set -eu

if [ $# -ge 2 ]; then
  CHANGED=$(git diff --name-only "$1" "$2")
else
  BASE=${1:-$(git merge-base origin/main HEAD)}
  CHANGED=$( { git diff --name-only "$BASE"; git ls-files --others --exclude-standard; } | sort -u)
fi

CODE_CHANGED=$(echo "$CHANGED" | grep -E '^(main\.go|internal/|static/|playbooks/|install\.sh|Makefile)' || true)
if [ -z "$CODE_CHANGED" ]; then
  echo "No code files changed — docs check skipped."
  exit 0
fi

echo "Code files changed:"
echo "$CODE_CHANGED"
echo ""

MISSING=""
echo "$CHANGED" | grep -q '^README\.md$'       || MISSING="$MISSING README.md"
echo "$CHANGED" | grep -q '^docs/index\.html$' || MISSING="$MISSING docs/index.html"

if [ -z "$MISSING" ]; then
  echo "All required doc files updated."
  exit 0
fi

echo "ERROR: Code changed but the following doc files were not updated:"
for f in $MISSING; do echo "  - $f"; done
echo ""
echo "Update these files, or (in CI) ask the maintainer for the 'skip-docs-check' label."
exit 1
