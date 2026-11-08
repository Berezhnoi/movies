# CLAUDE.md

Be extremely concise. No fluff, no long code snippets.

Go module `homework` (Gin + GORM + Postgres): movie catalog JSON API (JWT/RBAC) + HTMX web UI, one binary. CI-graded homework. Routes/config/security: `README.md`.

## Layout

- `cmd/api` server, `cmd/seed` first admin
- `internal/app` router wiring + most integration tests
- `internal/handler` JSON API; `handler/web` UI handlers + middleware
- `internal/service` business rules; `internal/repository` GORM + in-memory
- `internal/{auth,middleware,apperr,config,model}` JWT, RBAC, error mapping, env config, models
- `internal/chat` WebSocket hub; `internal/webhook` signed events; `internal/storage` trailers (S3/in-memory)
- `web/` embedded templates + static; `migrations/` SQL; `docs/` swagger (generated)
- `functions/notifier` separate module (Lambda); `infra/` VM/Terraform/K3s (`make -C infra`)

## Commands

`make` loads `.env`; most targets fail without it.

- Test (CI): `go test -race -count=1 ./...` (in-memory, no DB); single: `go test -run TestX ./internal/app`
- `make test-integration` — Postgres; `TEST_DB_NAME` must end in `_test`
- `go vet ./...`, `make fmt`, `make swagger` (after changing handler annotations)
- `make build && make verify-isolation`; `make run`; `make migrate-create name=x`

**CI fails on:** gofmt, `go vet`, `go test -race` (180s timeout).

## Never

- Change API contracts: routes, status codes, error JSON `{"error":{"code","message"}}`, roles.
- Read or edit `.env*`; `git push`; `terraform apply/destroy` (user does these).
- Hand-edit `docs/docs.go`/`docs/swagger.*` (run `make swagger`) or vendored `htmx.min.js`.

## Architecture

- Wiring in `internal/app/app.go`. `app.NewRouter()` = unprotected (in-memory repo, no auth, no UI) used by contract tests `movies_*_test.go`. `NewRouterWithDeps` (main) = secure: GORM, JWT/RBAC, Swagger, UI if `WEB_UI_ENABLED`; tested by `rbac_test.go`/`web_*_test.go`. Routing/middleware changes affect both — run both.
- `handler` → `service` → `repository`. In-memory and GORM repos must behave identically. RBAC = which group (`public/protected/staff/admin`) a route registers on. Return typed service errors; `apperr.WriteError` maps to HTTP — no status codes in handlers.
- Auth: API = Bearer header only, never cookies. UI = `access_token` HttpOnly cookie + CSRF + Origin check. CSP: no inline JS/CSS → `web/static/js/app.js`.
- Templates: one set per page (`title`/`content` blocks) + partial set for HTMX. `html/template` only, no `template.HTML` etc. (enforced by `web/audit_test.go`). HTMX delete → empty 200.
- Chat: hub single-owner (only `Run` touches `rooms`). Broker local or Redis (`REDIS_URL`); Redis tests need `TEST_REDIS_URL`.
- Webhook fires only on admin web registration. Config env-only, strict booleans, `JWT_SECRET` ≥32 bytes.

## Token economy

- Hooks run gofmt + `go vet` after each Go edit and a quick check on stop. Don't repeat them; run only targeted `-run` tests.
- Grep before Read; read only needed line ranges. Don't re-read after editing. Pipe long output through `tail`.
- "How/why does X work" → `docs-explorer`. Review → `go-reviewer`. Issue work → `/implement-issue`.
