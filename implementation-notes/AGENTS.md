# Agent Development Guide: Movies API

Guidance for AI coding agents working in this repository. For the full specification, see
[`PROJECT_SPEC.md`](./PROJECT_SPEC.md). The Claude Code equivalent is the root `CLAUDE.md`.

## Hard rules

- **Do not edit the official grading files:** `internal/app/stage*_test.go`, `internal/app/helpers_test.go`,
  `internal/app/variants_test.go`, `scripts/score.py`, and `.github/workflows/ci.yml`. They are checksummed
  in `.github/tests.sha256`. Change the implementation so the tests pass.
- **CI must stay green:** `gofmt`, `go vet ./...`, and `go test -race -count=1 -timeout 180s ./...`.
- **Keep both router modes working.** `app.NewRouter()` (contract tests) registers the API without auth and
  with in-memory repositories. `app.NewRouterWithDeps()` (production) adds JWT/RBAC and the web UI. Wiring
  for both lives in `internal/app/app.go`.
- **Keep repositories equivalent.** Every repository interface has an in-memory and a GORM implementation.
  Tests mostly use in-memory and production uses GORM, so a behavior change must land in both.
- **Errors:** return typed service errors (`ValidationError`, `NotFoundError`, `ErrForbidden`, ...) and let
  `apperr.WriteError` map them. Do not hand-write error JSON in handlers.
- **RBAC:** enforce access by registering a route on the right group (`public`, `protected`, `staff` =
  editor/admin, `admin`) in `handler.RegisterAPI` or the web route groups. Roles are `viewer`, `editor`, and
  `admin` (`internal/model`).
- **Auth surfaces stay separate.** The JSON API authenticates only from `Authorization: Bearer`. The web UI
  authenticates only from the `access_token` cookie (HttpOnly, SameSite=Lax, Secure via `COOKIE_SECURE`),
  and state-changing web requests need the CSRF token and a matching Origin.
- **Templates:** use `html/template` only. Do not use trusted types (`template.HTML` etc.), inline scripts,
  inline styles, or event-handler attributes. The CSP blocks them, and `internal/handler/web/audit_test.go`
  rejects them. Put JS in `web/static/js/app.js`.
- **Isolation:** templates and assets must stay embedded with `go:embed`. Do not read files from the source
  tree at runtime. `make verify-isolation` checks this.

## Adding a feature

1. **Schema:** add a migration pair with `make migrate-create name=...`, and update `internal/model`.
2. **Repository:** extend the interface, then implement it in both the in-memory and GORM versions.
3. **Service:** add the business rules and validation in `internal/service`.
4. **Handler:** add JSON handlers in `internal/handler` (with swag annotations, then `make swagger`), and
   web handlers in `internal/handler/web`.
5. **Templates:** update `web/templates` (the layout, `pages/`, and `partials/` for HTMX fragments).
6. **Tests:** add tests next to the code. Add router-level tests to `internal/app/*_test.go`, but never to
   the official files listed above.

## Commands

```sh
go test -race -count=1 ./...                           # full suite, no DB
go test -race -count=1 ./internal/app -run TestStage3  # subset
go vet ./... && make fmt
make test-integration                                  # PostgreSQL repository tests
make db-up && make migrate-up && make seed-admin && make run
make build && make verify-isolation
```

`make` targets load `.env`, so copy `.env.example` first. `functions/notifier` is a separate Go module;
run its tests from that directory.
