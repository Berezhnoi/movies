#!/usr/bin/env bash
# Format the edited Go file, then vet its package. vet errors go back to Claude.
f=$(jq -r '.tool_input.file_path // empty')
case "$f" in *.go) ;; *) exit 0 ;; esac
[ -f "$f" ] || exit 0

gofmt -w "$f"

out=$(cd "$(dirname "$f")" && go vet . 2>&1) || {
  echo "go vet failed for $(dirname "$f"):" >&2
  echo "$out" | head -30 >&2
  exit 2
}
exit 0