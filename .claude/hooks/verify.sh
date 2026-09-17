#!/bin/sh
# Stop hook: build and run the unit tests. Never blocks. On failure it writes the
# output to .claude/hooks/last-verify.log and tells the agent to read it.
cd "${CLAUDE_PROJECT_DIR:-.}" || exit 0
log=.claude/hooks/last-verify.log
if { go build ./... && go test ./...; } >"$log" 2>&1; then
  rm -f "$log"
  exit 0
fi
printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"Stop","additionalContext":"go build or go test failed. Read .claude/hooks/last-verify.log, fix the cause, then run make check."}}'
exit 0
