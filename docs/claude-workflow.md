# Claude Code workflow (token habits)

Everything auto-loaded (CLAUDE.md, skill/agent/command descriptions, MCP tool names) is paid on every turn; the conversation grows with every file read and command output. Keep both small.

## Sessions

- One issue or task per session; `/clear` between tasks.
- When a session gets long: `/compact keep: current issue, changed files, failing tests` (always give a focus).
- Use plan mode (shift+tab) for multi-file changes; approve the plan before edits start.
- Check cost with `/context` (what is loaded) and `/usage`; history with `npx ccusage@latest daily`.

## Asking

- Ask for a diff summary or short report, not full files or long explanations.
- Name the file and function when you know them; it saves a search.
- Never paste long logs; paste the first failure or save the log to a file and give its path.

## Exploring and checking

- Wide search ("where is X used", "how does Y work") → subagent (`docs-explorer`, `Explore`); only its summary enters the main context.
- Start from `docs/INDEX.md`; `docs/archive/` only when asked.
- Generated/vendored files (`docs/docs.go`, `docs/swagger.*`, `go.sum`, `htmx.min.js`) are read-denied; regenerate with `make swagger`.
- `make check` = quiet gofmt + vet + `-race` tests (before commit). Avoid `go test -v ./...` (~170 KB output).
- The Stop hook runs `scripts/check.sh --fast` (no `-race`) and skips when no `.go` file changed.

## Model per task

| Task | Use | Model |
|---|---|---|
| Doc / "how does it work" lookup | `docs-explorer` agent | haiku |
| Smoke-test running API | `api-tester` agent | haiku |
| Pre-commit review of diff | `go-reviewer` agent | sonnet |
| Security audit | `/security-check` | session model |
| Implementing / design | main session | strongest model; `/model` to switch |
