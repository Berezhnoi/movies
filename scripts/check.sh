#!/bin/sh
# Quiet gofmt + go vet + go test. Prints a one-line summary, or only the first failures.
#   scripts/check.sh          full check with -race (manual / pre-commit, same as CI)
#   scripts/check.sh --fast   no -race; skips when no .go file is changed (Stop hook)
set -u

cd "$(dirname -- "$0")/.." || exit 1
mode=${1:-full}
lines=20

if [ "$mode" = "--fast" ] && [ -z "$(git status --porcelain -- '*.go' 2>/dev/null)" ]; then
	echo "check: skipped (no changed .go files)"
	exit 0
fi

out=$(gofmt -l . 2>&1)
if [ -n "$out" ]; then
	echo "check: gofmt FAILED, run make fmt:"
	echo "$out" | head -n "$lines"
	exit 1
fi

if ! out=$(go vet ./... 2>&1); then
	echo "check: go vet FAILED:"
	echo "$out" | head -n "$lines"
	exit 1
fi

race=-race
[ "$mode" = "--fast" ] && race=
# shellcheck disable=SC2086 # $race is intentionally empty in fast mode
if ! out=$(env -u TEST_DATABASE_URL go test $race -count=1 -timeout 180s ./... 2>&1); then
	echo "check: go test FAILED:"
	echo "$out" | grep -E '^(--- FAIL|FAIL|panic:|.*_test\.go:[0-9]+:|.*DATA RACE)' | head -n "$lines"
	exit 1
fi

echo "check: ok (gofmt, vet, $(echo "$out" | grep -c '^ok') packages tested${race:+ with -race})"
