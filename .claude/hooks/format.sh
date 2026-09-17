#!/bin/sh
# PostToolUse hook for Edit and Write: gofumpt the edited file when it is Go source.
# The tool input arrives as JSON on stdin; the file path is extracted without jq.
file=$(sed -n 's/.*"file_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
case "$file" in
  *.go) ;;
  *) exit 0 ;;
esac
[ -f "$file" ] || exit 0
cd "${CLAUDE_PROJECT_DIR:-.}" || exit 0
exec go tool -modfile=tools/go.mod gofumpt -w "$file"
