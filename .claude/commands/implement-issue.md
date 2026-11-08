---
description: Implement one GitHub issue from a feature plan (research, tracer, build), run its checks, make one commit, and stop.
argument-hint: [issue number]
allowed-tools: Read, Grep, Glob, Edit, Write, Bash(gh issue view:*), Bash(git status:*), Bash(git branch:*), Bash(git diff:*), Bash(git log:*), Bash(git add:*), Bash(git commit:*), Bash(go:*), Bash(gofmt:*), Bash(make:*)
---

Implement GitHub issue #$ARGUMENTS on the current branch.

Current state:
!`git branch --show-current`
!`git status --short`

Rules:
- If the working tree has unrelated uncommitted changes, stop and tell me. Do not switch branches, fetch or push; I do that myself.
- Read the issue with `gh issue view $ARGUMENTS`. Read the PRD and plan files it links (docs/prd, docs/plans). If the issue depends on another issue that is not done, stop and say so.
- Follow the project patterns and CLAUDE.md. Existing contracts must not change (NewRouter(), Movie JSON, error format, env var names). Keep the code simple. Extend existing files instead of creating parallel ones.
- Do not read or edit .env files. If the issue needs a change to .env.example, write the exact lines in your report for me to add.
- Delete any test data you create in the dev database before finishing.

Steps:
1. Research: read the files the issue lists and anything they depend on. Report in a few lines what exists and what is missing.
2. Tracer: write the smallest failing test for the first step, then make it pass.
3. Build: do the remaining steps and add the tests the issue lists, including the negative cases.
4. Verify: run the issue's Verify command, then `gofmt -l .`, `go vet ./...` and `go test -race -count=1 ./...`. Say clearly which tests or checks you could not run (for example sandbox limits, browser, K3s). Do not claim anything is verified that you did not run.
5. Commit once, with a message that ends with "Refs #$ARGUMENTS". Do not push.

Finish with: the files changed, the test output, anything not verified, and decisions you made that the issue did not cover. Then stop.