# Movies API

A Go REST API for managing movies, backed by PostgreSQL. It includes JWT authentication, role-based access
control, database migrations, generated Swagger documentation, and in-memory contract tests.

## Run locally

Prerequisites: Go 1.21+, Docker Compose, and `make`.

```sh
cp .env.example .env
# Set local database, JWT, and admin credentials in .env.
make db-up
make migrate-up
make seed-admin
make run
```

The API listens on `http://localhost:8080` by default. Swagger UI is available at
`http://localhost:8080/swagger/index.html`.

## Run with Docker Compose

The API can also run in a container alongside PostgreSQL. Copy `.env.example` to `.env` and set the
database, pgAdmin, and JWT secrets before starting the stack. The backend reads its configuration from
that file and connects to PostgreSQL at the Compose service name `db`.

```sh
docker compose build backend
docker compose up -d db
make migrate-up
docker compose up -d backend
```

The backend is available at `http://localhost:${APP_PORT:-8080}` (normally `http://localhost:8080`).
pgAdmin remains available at `http://localhost:5050`. The existing database and pgAdmin volumes are
preserved. To start the database, pgAdmin, and backend together after applying migrations, use
`docker compose up -d db pgadmin backend`.

## API overview

| Method | Endpoint | Access |
|---|---|---|
| `POST` | `/api/v1/auth/register` | Public |
| `POST` | `/api/v1/auth/login` | Public |
| `GET` | `/api/v1/movies` | Public |
| `GET` | `/api/v1/movies/{id}` | Public |
| `GET` | `/api/v1/me` | Authenticated |
| `POST`, `PUT` | `/api/v1/movies`, `/api/v1/movies/{id}` | Editor or admin |
| `DELETE` | `/api/v1/movies/{id}` | Admin |
| `GET` | `/api/v1/users` | Admin |
| `PUT` | `/api/v1/users/{id}/role` | Admin |

Public registrations receive the `viewer` role. Access tokens use HS256 and the configured `JWT_TTL` (24 hours
by default); send them as `Authorization: Bearer <token>`. Set `JWT_SECRET` to a private value of at least
32 bytes. For setup details and the access matrix, see the [implementation notes](./implementation-notes/README.md).

The browser UI stores its access token in an HttpOnly cookie. `COOKIE_SECURE` defaults to `false` for
local `http://localhost` development; it **MUST** be set to `true` when the UI is served behind HTTPS.

## Web UI

The server-rendered UI and JSON API run in the same binary. `make run` starts both (the default,
`WEB_UI_ENABLED=true`); `make run-api-only` sets `WEB_UI_ENABLED=false` and serves only the API. The UI
uses the existing movie and authentication services. Its routes are:

| Page or action | Route | Access |
|---|---|---|
| Login / registration | `GET, POST /login`, `GET, POST /register` | Public |
| Catalog | `GET /`, `GET /catalog` | Public catalog; anonymous users are redirected to login |
| Live catalog rows | `GET /web/movies` | Login required |
| Create movie | `GET, POST /movies/new` | Editor or admin |
| Edit movie | `GET, POST /movies/{id}/edit` | Editor or admin |
| Delete movie | `DELETE /web/movies/{id}` | Admin only |
| Logout and assets | `POST /logout`, `GET /static/{path}` | Authenticated logout; public assets |

Browser login puts the JWT in the `access_token` HttpOnly, SameSite=Lax cookie. JavaScript cannot read
that cookie, unlike a token in localStorage, so an XSS flaw cannot directly extract the bearer token.
State-changing browser requests also require a CSRF token and an Origin host check. `COOKIE_SECURE`
controls the cookie Secure flag: it defaults to `false` for local HTTP, and must be `true` behind HTTPS
so browsers send the cookie only over encrypted connections. The JSON API remains header-only and accepts
`Authorization: Bearer <token>`; it does not authenticate from browser cookies.

| UI action | Anonymous | Viewer | Editor | Admin |
|---|---:|---:|---:|---:|
| Browse/search catalog | Redirect to login | Yes | Yes | Yes |
| Create/edit movies | Redirect to login | 403 | Yes | Yes |
| Delete movies | 401 for HTMX / login redirect | 403 | 403 | Yes |
| Register accounts | Public registration | No | No | Yes |

HTMX is vendored locally (2.0.4; SHA-256 `e209dda5c8235479f3166defc7750e1dbcd5a5c1808b7792fc2e6733768fb447`): live search replaces
the movie rows, and admin delete removes a row after an empty 200 response. Search, pagination, and forms
also work without JavaScript; after a live search the stale pager is hidden until Enter reloads the results.

Templates live under `web/templates`: a shared layout, reusable partials, and page templates. The renderer
parses one template set per page because Go template definitions share a namespace inside a set; separate
sets prevent pages' common `title` and `content` blocks from overwriting one another. A separate partial
set renders HTMX fragments. `go:embed` includes templates and assets, `template.ParseFS` parses templates,
and `fs.Sub(staticFS, "static")` serves the asset subtree without a duplicated `/static` segment.

Build the self-contained UI binary with `make build` and run `make verify-isolation` to copy that binary
into a clean temporary directory and request embedded pages/assets. This proves the UI does not need its
source tree or loose template/static files at runtime; PostgreSQL remains an external runtime dependency.
The relevant environment variables are `WEB_UI_ENABLED` (strict boolean, default `true`) and
`COOKIE_SECURE` (strict boolean, default `false`; set `true` for HTTPS). The current Origin check compares
hosts; deployments behind a TLS-terminating proxy should also validate the configured public scheme/origin.

The release-status badge reflects release year: Upcoming, New (within two years), or Classic; an unknown
year displays `-`. Known limitations: a role change does not invalidate an existing JWT, there is no rate
limiting, PostgreSQL is still external to the binary, and the pager is hidden after live search because its
links refer to the previous result set.

## Movie chat

Each movie has a real-time chat room over WebSockets. `GET /ws/movies/{id}` upgrades after checking the JWT
(web UI cookie or `Authorization: Bearer`), the movie ID, and the `Origin`; the page is `/movies/{id}/chat`.
`WS_ALLOWED_ORIGINS` (comma-separated, default `http://localhost:8080`) lists the allowed browser origins.
To try it, log in at `http://localhost:8080`, open `/movies/1/chat` in 3 tabs, and send a message from one.
The hub keeps rooms in memory, so it works with a single replica only; several replicas would need Redis pub/sub.

## Lesson 10 course checklist

| Item | Where | OK |
|---|---|---|
| HttpOnly-cookie rationale and localStorage XSS risk | `implementation-notes/design_v10.txt`, section 2 | Yes |
| Manual audit: `text/template` imports | `internal` and `web` Go sources: none; audit test checks AST imports | Yes |
| Manual audit: `template.HTML`, `JS`, `CSS`, `URL`, `HTMLAttr` and related trusted types | `internal` and `web` Go sources: none; audit test checks Go AST | Yes |
| Manual audit: inline event handlers/scripts and unsafe template pipelines | `web/templates/**/*.html`: none | Yes |
| Single-binary isolation | `make verify-isolation` | Yes, when run successfully |
| Embedded FS setup | `web/templates.go`, `web/static.go`, and wiring in `cmd/api/main.go` | Yes |
| Layout content block and movie-row partial | `web/templates/layout.html`, `web/templates/partials/movie_row.html` | Yes |
| Exact live-search attributes | `web/templates/pages/catalog.html` | Yes |
| Exact delete attributes and empty 200 handler | `web/templates/partials/movie_row.html`, `internal/handler/web/catalog.go` | Yes |
| Required catalog cases | `internal/handler/web/catalog_test.go` | Yes |
| Lesson 9 Tasks 2.1 and 2.2 retained | `implementation-notes/prompts.txt` | Yes |
| JWT only in HttpOnly access cookie; contextual autoescaping retained | `internal/handler/web/auth.go`, templates, and audit test | Yes |

## Development

```sh
go test -race -count=1 ./...
go vet ./...
make swagger
```

The standard test suite does not require a database. PostgreSQL integration tests run separately with
`make test-integration` and use the isolated `_test` database configured in `.env`.

## Cloud lab (VM + Floci + K3s)

Everything runs inside the VirtualBox VM from `infra/vm/Vagrantfile` (Docker, Go, Terraform, aws cli, K3s).
Step-by-step from scratch: [`infra/README.md`](./infra/README.md).
When an admin creates a user in the web UI (`POST /register`), the app sends a signed `user.created`
webhook to a Go Lambda running on Floci (local AWS emulator, port 4566). The Lambda sends a mock
confirmation email through Floci's SES. Public API registration does not trigger it.

```sh
make -C infra secrets     # generate infra/secrets.env (gitignored, mode 600)
make -C infra all         # Floci up, build + test Lambda, terraform apply, build image, deploy to K3s
make -C infra e2e         # admin creates a user -> email must appear in Floci SES
make -C infra mailbox     # show captured emails (GET /_aws/ses)
```

The app is at `http://localhost:30080` (NodePort) inside the VM. Host port 8080 forwards to the K3s Traefik ingress on guest port 80; see `docs/k3s-notes.md`.

| Piece | Where | Notes |
|---|---|---|
| Webhook trigger | `internal/webhook`, wired in `cmd/api/main.go` | Off unless `WEBHOOK_URL` is set; HMAC-SHA256 over `timestamp.body`; async, never changes the response |
| Lambda | `functions/notifier` (separate Go module) | `provided.al2023`; verifies signature and 5-minute replay window; sends via SES v2 |
| Terraform | `infra/terraform` | Floci endpoint only, dummy `test` credentials; state and tfvars gitignored |
| K3s | `infra/k8s` | Postgres, migrate + seed jobs, app on NodePort 30080; secrets come from `infra/secrets.env` |
| Floci findings | `docs/floci-notes.md` | Cold start, Function URL routing by Host header (CoreDNS workaround), SES quirks |
