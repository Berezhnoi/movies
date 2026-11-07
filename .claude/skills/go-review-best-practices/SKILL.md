---
name: go-review-best-practices
description: Review Go code and apply Go best practices in the Movies project: layering, errors, context, concurrency, security, tests. Use for code reviews, pre-commit checks and cleanup of Go changes.
---

# Go code review and best practices (Movies project)

Use this skill when asked to review Go code, check a change before a commit, or improve code quality. The project is a study project: prefer simple code, small diffs and no new abstractions or dependencies without a clear reason.

## Rules
- Review first, change later. Do not edit files during a review unless asked to fix.
- Never change behaviour or contracts while cleaning up: REST routes and status codes, the error JSON `{"error":{"code","message"}}`, auth and roles, the gRPC proto, the chat protocol, env var names. If a fix would change one, list it as "needs decision".
- Skip generated code (`*.pb.go`, `gen/`).
- Every finding needs `path:line`, the problem, and a concrete fix. Do not report what you did not verify in the code.

## Process
1. Scope: the path, package or feature given. If none, use `git diff` and `git status`.
2. Baseline: run `gofmt -l .`, `go vet ./...`, `go build ./...`, `go test -race ./...` (every Go module, including `lambda/*`). Use `staticcheck` or `golangci-lint` only if already installed.
3. Read the code against the checklist below.
4. Report findings sorted by severity: blocker / should fix / nit.
5. If asked to fix: small logical groups, re-run build, vet and `go test -race` after each group, and add tests for each fixed bug.

## Checklist

### Project conventions
- Layers: handler -> service -> repository interface. No DB or HTTP code in the wrong layer. Handlers only parse, validate and map errors.
- `ctx context.Context` is the first parameter and is passed down; never `context.Background()` inside a request or stream.
- Interfaces are small and defined where they are used. No interface with a single implementation unless it is a repository.
- Constructors take dependencies explicitly. No package-level mutable state, no `init()` side effects.
- New features are additive (new constructors, new files); existing tests and `NewRouter()` keep working.

### Errors
- No ignored errors (`_ =` needs a reason). Wrap with `fmt.Errorf("...: %w", err)`, check with `errors.Is/As`.
- Handlers return the project's error JSON and the right status (400 invalid id, 401 unauthenticated, 403 forbidden, 404 not found, 422 validation).
- Never leak internals (SQL, stack traces, file paths) in responses.
- gRPC handlers use `status.Errorf` with `codes.*`, never plain errors.
- No `panic` for expected failures; recover only at the edge.

### Concurrency
- Every goroutine has one clear exit path tied to a context or a channel close.
- Shared state has one owner (single goroutine) or a mutex used everywhere. Maps are never read and written concurrently.
- A channel is closed by one place only, once. Sending on a closed channel is impossible by design.
- Slow consumers cannot block producers (bounded buffers, drop or disconnect).
- `go test -race` passes; tests count goroutines to catch leaks.

### Security
- No secrets in code, logs, Dockerfiles or git history; `.env`, `*.tfstate`, `*.tfvars` and keys are git-ignored.
- JWT: HS256 secret from the environment (min 32 bytes), expiry checked, role check on every protected route (auth middleware before role middleware). Identical error for unknown user and wrong password.
- Passwords use bcrypt; never returned in JSON (`json:"-"`). DTOs prevent mass assignment (role, id).
- All input validated (type, range, length). SQL only through GORM parameters or `?` placeholders.
- CORS and WebSocket `CheckOrigin` use an allowlist, never `return true`.
- Webhooks verify HMAC signature and timestamp with constant-time comparison.
- Plaintext gRPC and mock credentials only for local development, and said so in a comment.

### Database
- Migrations apply in order and are reversible; constraints and indexes match the queries.
- No N+1 queries (use Preload or joins). Transactions where several writes must succeed together.
- Connection pool settings are set. Rows are closed; errors from `rows.Err()` are checked.

### HTTP, gRPC, WebSocket
- Pagination has limits; list endpoints never return unbounded data.
- Streaming handlers watch `stream.Context().Done()`.
- WebSocket: one writer per connection (the write pump), ping/pong with deadlines, `SetReadLimit`, author taken from the JWT, never from the client.

### Tests
- Table-driven tests with clear case names; `t.Helper()` in helpers; `t.Cleanup` for teardown.
- Cover negative paths (401 vs 403, invalid input, unknown id, boundaries), not only the happy path.
- No `time.Sleep` for synchronization: poll with a timeout.
- Integration tests are gated by an env var (e.g. `TEST_DATABASE_URL`, `TEST_REDIS_URL`) and skip when unset.
- Use `httptest` for HTTP and WebSocket, `bufconn` for gRPC. File names end in `_test.go` and test names start with `Test`.
- Do not write tests that only repeat the implementation.

### Style and simplicity
- `gofmt` clean; short lowercase package names; exported names have a doc comment; comments explain WHY, not WHAT.
- Early returns, short functions, plain names. No dead code, no leftover debug prints, no commented-out blocks.
- Standard library first; every dependency must be justified.

## Output format
Start with a summary table (count of blockers / should fix / nits, vet and test status). Then list each finding as:

`[severity] path:line - problem. Fix: ...`

End with one verdict: "OK to commit" or "Fix blockers first", plus a "needs decision" list for anything that would change behaviour or a contract.