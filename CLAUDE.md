# CLAUDE.md

Keep your replies extremely concise and focus on conveying the key information. No unnecessary fluff, no long code snipepts.

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Go (module `homework`, Gin + GORM + PostgreSQL) movie catalog: JSON API with JWT/RBAC plus a server-rendered HTMX web UI, shipped as one binary. This is a course homework repo graded by CI. See `README.md` for routes, config and the security model.

## Commands

`make` includes and exports `.env` (copy from `.env.example` first), so most targets fail without it.

```sh
VARIANT=movies go test -race -count=1 ./...          # full suite; in-memory, no DB needed (what CI runs)
VARIANT=movies go test -race -count=1 ./internal/app -run TestStage3 # single test / group
make test                                            # same (Makefile defaults VARIANT=movies), unsets TEST_DATABASE_URL
make test-integration                                # Postgres repository tests; needs TEST_DB_NAME ending in _test
go vet ./...
make fmt                                             # gofmt -w
make swagger                                         # regenerate docs/ (swag v1.16.4) after changing handler annotations
make build && make verify-isolation                  # static binary in bin/, then run it from an empty dir to prove embeds work
make db-up && make migrate-up && make seed-admin && make run   # local stack (API + UI on :8080)
make migrate-create name=add_foo                     # new sequential up/down migration pair
```

`functions/notifier` is a separate Go module (Lambda); test it from that directory. Cloud lab (VM, Floci, Terraform, K3s) is driven by `make -C infra ...` — see `infra/README.md`.

## Grading constraints (do not break)

- `internal/app/stage*_test.go`, `helpers_test.go`, `variants_test.go`, `scripts/score.py`, and `.github/workflows/ci.yml` are official files checksummed in `.github/tests.sha256`. Never edit them; change the implementation instead.
- These tests select expected behavior from the `VARIANT` env var (`movies`). There is no `VARIANT` file; CI sets the env var, and `.github/tests.sha256` was regenerated after that ci.yml change. Regenerate it with `sha256sum` if any official file changes.
- CI fails on any of: gofmt, `go vet`, or `go test -race` with a 180s timeout.

## Architecture

**Wiring lives in `internal/app/app.go`**, not in `cmd/`. The contract tests talk to the API only through `app.NewRouter()`, which builds an **unprotected** router (`Deps.Secure=false`): an in-memory movie repo, all API routes registered without auth middleware, and no web UI. `cmd/api/main.go` uses `NewRouterWithDeps`, which forces `Secure=true`: GORM repositories, JWT/RBAC route groups, Swagger, and the web UI when `WEB_UI_ENABLED`. Changing routing or middleware can therefore affect both modes. Check both test paths (`stage*_test.go` for unprotected and `rbac_test.go`/`web_*_test.go` for secured).

Layering: `handler` → `service` → `repository` (interfaces with both in-memory and GORM implementations; keep them behaviorally equivalent, since tests use in-memory and production uses GORM). `handler.RegisterAPI` takes `public/protected/staff/admin` Gin groups, so RBAC is applied by which group a route is registered on (`middleware.RequireRole`). Service errors (`ValidationError`, `NotFoundError`, `ErrForbidden`, ...) map to HTTP status and JSON in `internal/apperr.WriteError`; return those typed errors rather than writing status codes in handlers.

Catalog search is a separate `MovieSearcher` capability (`repository.NewMovieSearcher`) with GORM, in-memory, and fallback implementations, used by `service.CatalogService` for the web UI.

**Two auth surfaces:**
- JSON API: `Authorization: Bearer` header only; it never authenticates from cookies.
- Web UI (`internal/handler/web`): JWT in the `access_token` HttpOnly, SameSite=Lax cookie (`COOKIE_SECURE` controls Secure). Its own middleware handles CSRF tokens + Origin host check on state-changing requests, `OptionalAuth`, and `RequireRole`. CSP forbids inline scripts/styles, so put JS in `web/static/js/app.js`, not in templates.

**Templates**: `web/` embeds templates and static assets via `go:embed`. The renderer parses one template set per page (pages share `title`/`content` block names) plus a separate partial set for HTMX fragments. Use only `html/template` and no trusted types (`template.HTML` etc.). `internal/handler/web/audit_test.go` enforces this via AST checks. HTMX is vendored (`web/static/js/htmx.min.js`, see `VENDORED.txt`). HTMX delete must return an empty 200.

**Chat**: `internal/chat` hub is single-owner (only `Run` touches `rooms`; others send on channels). Room messages go through a `broker`: local (default) or Redis when `REDIS_URL` is set (pub/sub `chat:movie:*` + capped history list). Redis tests in `internal/app/chat_redis_test.go` skip unless `TEST_REDIS_URL` is set; `docker compose --profile scale up` runs two instances on :8081/:8082.

**Webhook**: `internal/webhook` decorates the `UserRepository` when `WEBHOOK_URL` is set and sends an HMAC-signed async `user.created` event. It fires only for admin web registration (`MarkAdminRegistration` wraps the handler), not for public API registration.

Config is env-only (`internal/config`). Booleans like `WEB_UI_ENABLED` and `COOKIE_SECURE` are parsed strictly. `JWT_SECRET` must be ≥32 bytes.
