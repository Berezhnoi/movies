#!/usr/bin/env bash
# Runs when Claude wants to stop. A failing quick check makes it continue.
input=$(cat)
# Avoid an endless loop: if we already forced a continuation, let it stop.
[ "$(echo "$input" | jq -r '.stop_hook_active')" = "true" ] && exit 0

cd "$CLAUDE_PROJECT_DIR" || exit 0
out=$(./scripts/check.sh --fast 2>&1) || {
  echo "Fix before finishing:" >&2
  echo "$out" >&2
  exit 2
}
exit 0
