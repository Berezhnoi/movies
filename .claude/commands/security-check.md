---
description: Security audit of the repo or a given path (secrets, auth, input, web, chat, infra, deps); reports findings only. For general code review use go-reviewer.
argument-hint: [path or feature, optional, e.g. internal/chat]
allowed-tools: Read, Grep, Glob, Bash(git status:*), Bash(git ls-files:*), Bash(git diff:*), Bash(go vet:*), Bash(govulncheck:*)
---

Run a security check of the Movies project. Scope: $ARGUMENTS (if empty, the whole repository).

Current state of the repo:
!`git status --short`

Report findings only. Do not edit any file. For depth on a topic, read only the matching file in the golang-security skill references/. Check these areas for the scope:

1. Secrets: hard-coded keys, passwords or tokens in code, Dockerfiles, compose files, k8s manifests and Terraform. Files that must not be tracked but are (`git ls-files`: .env, *.tfstate, *.tfvars, private keys). Check that .gitignore covers them. Never print a secret value; give only the file and line.
2. Auth: JWT secret from env and its minimum length, expiry check, role check on every protected route (list every route and its protection), identical errors for unknown user and wrong password, bcrypt usage, password never in JSON.
3. Input: validation of every request field, SQL built from strings, mass assignment (role, id), request size limits.
4. Web: CORS allowlist, CSRF and cookie flags (HttpOnly, SameSite, Secure) if cookies are used, CSP, template autoescaping.
5. Real-time: WebSocket CheckOrigin allowlist, author taken from the JWT, read limits; webhook HMAC with timestamp and constant-time compare.
6. Infra: containers running as root, exposed ports (Redis, Postgres, Floci bound to 127.0.0.1?), Terraform IAM wildcards, real AWS guard.
7. Dependencies: run `govulncheck ./...` if it is installed (do not install it) and report the result; otherwise say it was skipped.

Output:
- A summary table: area, status (OK / issue / not applicable), number of findings.
- Findings sorted by severity (critical / high / medium / low), each as `path:line - problem. Fix: ...`.
- A "verified OK" list, so I can see what was actually checked.
- End with the 3 fixes I should do first.

Do not report what you did not verify in the code.