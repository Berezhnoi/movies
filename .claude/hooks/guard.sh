#!/usr/bin/env bash
# Exit 2 = block the action; the message on stderr goes back to Claude.
input=$(cat)
tool=$(echo "$input" | jq -r '.tool_name')

if [ "$tool" = "Bash" ]; then
  cmd=$(echo "$input" | jq -r '.tool_input.command // empty')
  if echo "$cmd" | grep -Eq 'terraform[[:space:]]+(apply|destroy)|git[[:space:]]+push|rm[[:space:]]+-rf[[:space:]]+/'; then
    echo "Blocked: terraform apply/destroy, git push and rm -rf / are done by the user, not the agent." >&2
    exit 2
  fi
else
  f=$(echo "$input" | jq -r '.tool_input.file_path // empty')
  case "$f" in
    *.pb.go|*/gen/*|*.env|*.tfstate|*.tfstate.*|*.tfvars|*/id_ed25519*)
      echo "Blocked: $f is generated or secret. Regenerate it, or edit it yourself." >&2
      exit 2 ;;
  esac
fi
exit 0