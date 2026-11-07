# Code Review — lesson-07

Date: 2026-11-02 · Branch: `chore/project-cleanup` · Scope: whole repository (not a diff).

Reviewed in parallel by area: (1) REST handlers/services/repositories/auth, (2) DB/migrations/Docker, (3) chat/WebSocket, (4) gRPC, (5) Lambda (`functions/notifier`) + `infra/`, plus a docs-vs-code check. Findings were merged and deduplicated; every **major** was re-checked in the code.

## Summary

- **Blockers: none.**
- **gRPC:** no gRPC code, `.proto` files or `cmd/grpc-*` exist (only `google.golang.org/protobuf` as an indirect dep). The area is N/A; only stale doc references remain (see D5).
- **Lambda:** lives in `functions/notifier` (there is no `lambda/` dir). HMAC contract with `internal/webhook` matches (`"<ts>.<body>"`, HMAC-SHA256, `hmac.Equal`, ±5 min window, ≥32-byte secret on both sides).
- **Tests:** `go vet ./...` clean; `go test -race` passes except tests that use `httptest.NewServer` (chat, webhook), which fail in the sandbox on port binding — re-run outside the sandbox. `functions/notifier` tests were not run (module cache unavailable) — run `make -C infra lambda-test`.

## Major

| # | Location | Issue | Failure scenario | Fix |
|---|---|---|---|---|
| M1 | `internal/handler/web/middleware.go:59` | `OptionalAuth` loads the user but sets the principal from `claims.Role`. | A demoted admin/editor keeps UI write/admin access until the cookie expires. | Use `user.Role`. |
| M2 | `internal/middleware/auth.go:35-36` | API trusts `role` from the JWT; user is never reloaded (TTL 24h). | Demoted or deleted admin can still delete movies / change roles for up to 24h. | Reload user and take role from DB, or add a `token_version` claim + short TTL. |
| M3 | `internal/service/user_service.go:60-70` | "Last admin" check (`CountByRole`) and `UpdateRole` are separate calls; lock covers one row only. | Two admins demote each other concurrently → zero admins. | Check + update in one transaction, locking admin rows (`FOR UPDATE`). |
| M4 | `internal/service/movie.go:95-101` | `release_year` parsed as any int, no range; DB column is `INT`. | `99999999999` → 201 in-memory, 500 on Postgres (repos diverge). | Validate a range (e.g. 1888–2200) → `ValidationError`. |
| M5 | `internal/chat/client.go:19`, `internal/chat/hub.go:112-117`, `client.go` readPump | No per-client rate limit; send buffer is 16 and a full buffer drops the client. | One user bursts ~20 messages and every other client in the room is disconnected (JS does not reconnect). | Per-connection limiter in readPump; optionally larger buffer. |
| M6 | `docker-compose.yml:15-16`, `:33-34` | Postgres `5432` and pgAdmin `5050` published on all interfaces (Redis correctly on `127.0.0.1`). | DB/pgAdmin reachable from the LAN or VM network. | Bind to `127.0.0.1:`. |
| M7 | `docker-compose.yml:5-6` | `db` service uses `env_file: .env`, so it receives `JWT_SECRET` etc.; `dpage/pgadmin4` is unpinned. | Secrets leak into an unrelated container; pgAdmin floats on `latest`. | Drop `env_file` from `db` (it sets `POSTGRES_*` explicitly); pin pgAdmin. |

## Minor

**Auth / REST**
- Web logout only clears the cookie, so the JWT stays valid. The CSRF token is not rotated on login and not bound to the session (`internal/handler/web/auth.go:106-109`, `middleware.go:65-81`). The Origin check mitigates this.
- Unsafe defaults: `COOKIE_SECURE=false` and CORS `*` (`internal/config/config.go:92,98`), and Swagger is always on (`cmd/api/main.go:129`).
- Only `ReadHeaderTimeout` is set and `ReadTimeout`/`IdleTimeout` are missing (`cmd/api/main.go:123-136`).
- API 500s and panics are not logged (`internal/apperr/errors.go:45-46`, `internal/middleware/http.go:35-49`).
- There is no rate limiting on login and register. API register reveals whether an email exists (409).
- The web middleware depends directly on `repository.UserRepository` and bypasses the service layer (`internal/handler/web/middleware.go:17,26`).

**DB**
- `UpdateWithError` reads the current row without `FOR UPDATE`, so concurrent updates can write the same `updated_at` (`internal/repository/movie_gorm.go:95-100`).
- `genres` and `directors` rows are never cleaned up after deletes (`migrations/000002`, `movie_gorm.go:229-254`).
- The ID is `BIGSERIAL` but the API uses `uint32`. The overflow check runs after insert (`movie_gorm.go:38-40`). This is theoretical.

**Chat**
- The token is checked only at the handshake, so the socket outlives JWT expiry or a demotion (`internal/chat/handler.go:46-90`).
- History is loaded before `register`, so a client can miss or duplicate messages in that window (`handler.go:67-85`).
- The Redis subscribe doesn't wait for `sub.Receive`. The loop exits silently and is never retried (`internal/chat/broker.go:56-74`).
- `Publish` uses `context.Background()` with no timeout. A partial failure can deliver the message locally twice (`hub.go:97`).
- There is no cap on connections per user and no `HandshakeTimeout` (`handler.go:62-93`).
- Shutdown doesn't wait for `chatHub.Run` before `rdb.Close()` (`cmd/api/main.go:121`).

**Background work**
- The webhook goroutine isn't tracked on shutdown and has no retries, so SIGTERM can drop events (`internal/webhook/webhook.go:182-193`).

**Docker / ops**
- There is no API healthcheck in the Dockerfile or compose (`Dockerfile:18-20`, `docker-compose.yml:44-58`).
- `golang:1.21-alpine` is EOL (`Dockerfile:1`, `infra/k8s/Dockerfile:3`). Bump it together with `go.mod`.
- The seed binary ships in the runtime image (`Dockerfile:20`).

**Lambda / infra**
- Replay protection is only the 5-minute window (`functions/notifier/handler.go:86-89`).
- The parsed email address isn't used. The raw string goes to SES (`handler.go:67,106`). Use `addr.Address`.
- The IAM policy allows `ses:SendEmail` on `*` (`infra/terraform/main.tf:11-15`). Scope it to the identity ARN.
- `app.yaml`, `postgres.yaml` and `jobs.yaml` in `infra/k8s/` are missing resources, a liveness probe, `seccompProfile` and `automountServiceAccountToken: false`. Postgres and the Jobs have no securityContext.
- The seed Job injects the whole `movies-secrets` secret, and the migrate Job puts the DB password in its args (`infra/k8s/jobs.yaml:16,45-47`).
- Images aren't pinned (`postgres:16-alpine`, `movies-api:lab`, SAM `releases/latest`). The Go tarball and the K3s `curl | sh` are not checksum-verified (`infra/vm/Vagrantfile:50,81,88`).

## Docs vs code

| # | Doc | Says | Code | Severity |
|---|---|---|---|---|
| D1 | `README.md:20` | `make db-up` starts Postgres + pgAdmin | `docker compose up -d` also starts `backend` on :8080 and Redis, which clashes with `make run` (`Makefile:14-15`) | major |
| D2 | `README.md:56` | `/register` is public | Admin-only (`internal/app/app.go` admin web group, `RequireRole("admin")`) | major |
| D3 | `README.md:57` | `/`, `/catalog` need login | Anonymous access is allowed; only `/movies/:id/chat` uses `RequireLogin` | major |
| D4 | `README.md:46` | `PUT /api/v1/movies` | Only `PUT /movies/:id` exists (`internal/handler/routes.go:14-15`) | minor |
| D5 | `.claude/agents/docs-explorer.md:3,12-13`, `api-tester.md:3,8`, `go-reviewer.md:12` | gRPC, `proto/*.proto`, `docs/security_audit.md`, etc. | None of these exist | minor |
| D6 | `README.md:74-78` | Config table | Missing `WEBHOOK_TIMEOUT`, `LOG_SQL`, `ALLOWED_ORIGIN`, `DB_*`, `APP_PORT`, and the ≥32-byte `WEBHOOK_SECRET` rule | minor |
| D7 | `CLAUDE.md` | "Config is env-only (`internal/config`)" | Env is also read in `internal/webhook/webhook.go:49-60`, `internal/middleware/http.go:52` and `cmd/api/main.go:51`. `cfg.AllowedOrigin` is unused | minor |
| D8 | `CLAUDE.md` | `make test` is the same as `-race -count=1` | `Makefile:50` has no `-race` and no `-count=1` | minor |
| D9 | `README.md` web table / layout | — | `/logout`, `/web/movies` and the `/api/v1/health` alias are undocumented. The layout omits `internal/{auth,middleware,apperr,model}`, `docs/` and `scripts/` | minor |
| D10 | `README.md:16`, `infra/README.md:16` | Clone URL `Berezhnoi/movies.git` | Remote is `Berezhnoi/lesson-07.git` | minor |
| D11 | `README.md:96` | Redis "without persistence" | Default RDB snapshots are on, but there is no volume, so data is lost on recreate | nit |
| D12 | `infra/README.md:59` vs `infra/k8s/config.yaml:18` | Lambda cold start "~20s" vs "~25s" | Inconsistent | nit |

## Nits

- Dead code: the `CatalogService.movies` field (`internal/service/catalog_service.go:20,26-28`).
- There are two parallel movie repo interfaces with type assertions in the service. The legacy bool methods swallow DB errors (`internal/repository/movie.go:12-28`, `internal/service/movie.go:196-240`).
- `PUT /users/abc/role` returns "Invalid movie id" (`internal/handler/user_handler.go:98`).
- There is a redundant allocation at `internal/handler/web/catalog.go:109-110`.
- Search uses `ILIKE` on Postgres and `strings.ToLower` in memory, so Unicode case folding can differ.
- Chat history keys have no TTL (`internal/chat/broker.go:50`). `JSON.parse` in `web/static/js/app.js:41` is unguarded and the message list grows without bound.
- The `users` PK is unnamed while the other tables use `pk_*` names. `release_year` has no DB `CHECK`. `migrate-down` has no confirmation.
- `lambda-test` runs without `-race` (`infra/Makefile:33`).

## Recommended order

1. M1 (one-word fix), M3 and M2, which cover authorization correctness.
2. M5 (chat flood) and M4 (release_year range, which also restores in-memory/GORM parity).
3. M6 and M7 (compose exposure), then D1–D3 in the README.
