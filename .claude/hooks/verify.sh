#!/usr/bin/env bash
# Runs when Claude wants to stop. Failing build/tests make it continue.
input=$(cat)
# Avoid an endless loop: if we already forced a continuation, let it stop.
[ "$(echo "$input" | jq -r '.stop_hook_active')" = "true" ] && exit 0

cd "$CLAUDE_PROJECT_DIR" || exit 0
out=$(go build ./... 2>&1 && VARIANT="${VARIANT:-movies}" go test ./... 2>&1) || {
  echo "Build or tests failed, fix before finishing:" >&2
  echo "$out" | tail -40 >&2
  exit 2
}
exit 0