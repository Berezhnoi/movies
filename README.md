# Movies

A movie catalog service in Go. It ships as one static binary that serves a JSON REST API and a
server-rendered web UI.

- **API:** Gin + GORM on PostgreSQL. JWT auth with three roles (viewer, editor, admin), SQL migrations,
  and Swagger docs.
- **Web UI:** `html/template` + HTMX. Templates and assets are embedded with `go:embed`, so the binary needs
  no extra files at runtime.
- **Movie chat:** a real-time chat room for each movie over WebSockets.
- **Integrations:** an optional HMAC-signed `user.created` webhook to a Go AWS Lambda that sends a
  confirmation email through SES.

## Quick start

Prerequisites: Go 1.21+, Docker Compose, `make`.

```sh
cp .env.example .env   # then set DB_PASSWORD, JWT_SECRET, ADMIN_EMAIL/ADMIN_PASSWORD
make db-up             # PostgreSQL + pgAdmin
make migrate-up
make seed-admin        # creates the first admin from ADMIN_EMAIL / ADMIN_PASSWORD
make run               # API + web UI on http://localhost:8080
```

- Web UI: <http://localhost:8080>
- Swagger: <http://localhost:8080/swagger/index.html>
- API only (no web routes): `make run-api-only`

To run the backend in a container instead:

```sh
docker compose build backend
docker compose up -d db && make migrate-up && docker compose up -d backend
```

## API

Send the token as `Authorization: Bearer <token>`. Public registration always assigns the `viewer` role.

| Method | Endpoint | Access |
|---|---|---|
| `POST` | `/api/v1/auth/register`, `/api/v1/auth/login` | Public |
| `GET` | `/api/v1/movies`, `/api/v1/movies/{id}` | Public |
| `GET` | `/api/v1/me` | Authenticated |
| `POST`, `PUT` | `/api/v1/movies`, `/api/v1/movies/{id}` | Editor, admin |
| `DELETE` | `/api/v1/movies/{id}` | Admin |
| `GET` | `/api/v1/users` | Admin |
| `PUT` | `/api/v1/users/{id}/role` | Admin |
| `GET` | `/health` | Public |

## Web UI

| Page | Route | Access |
|---|---|---|
| Login, registration | `/login`, `/register` | Public (admins can also register users) |
| Catalog with live search | `/`, `/catalog` | Logged in |
| Create, edit movie | `/movies/new`, `/movies/{id}/edit` | Editor, admin |
| Delete movie (HTMX) | `DELETE /web/movies/{id}` | Admin |
| Movie chat | `/movies/{id}/chat` (socket at `/ws/movies/{id}`) | Logged in |

Search, pagination and forms also work without JavaScript.

## Configuration

All settings come from environment variables. `.env.example` lists every variable with comments. The main
ones are:

| Variable | Default | Notes |
|---|---|---|
| `JWT_SECRET` | — | Required, at least 32 bytes |
| `JWT_TTL` | `24h` | |
| `WEB_UI_ENABLED` | `true` | `false` serves the API only |
| `COOKIE_SECURE` | `false` | **Must be `true` behind HTTPS** |
| `WS_ALLOWED_ORIGINS` | `http://localhost:8080` | Origins allowed to open the chat socket |
| `WEBHOOK_URL` / `WEBHOOK_SECRET` | empty | An empty URL turns the webhook off |

## Security

- Passwords are hashed with bcrypt. JWTs are signed with HS256 only and must include `exp`. Login failures
  return the same generic error, and login takes about the same time whether or not the email exists.
- Each route is placed in an auth group, so RBAC is decided in one place. Request and response DTOs keep
  password hashes and roles out of client input and output.
- The web UI keeps the JWT in an `HttpOnly`, `SameSite=Lax` cookie, so page scripts cannot read it.
  Requests that change data need a double-submit CSRF token and a matching `Origin`. The UI sends a strict
  CSP that blocks inline scripts and styles. The JSON API never reads auth from cookies.
- Templates use `html/template` contextual escaping. A test parses the source and fails if it finds
  `text/template` or trusted types such as `template.HTML`.

Known limitations:

- A role change takes effect only after the user's existing JWT expires.
- There is no rate limiting.
- The `Origin` check compares only the host, not the scheme.
- Chat rooms are kept in memory, so the chat works with a single replica only.

## Project layout

```
cmd/api, cmd/seed     server and first-admin seed command
internal/app          router wiring (API, auth groups, web UI, Swagger)
internal/handler      JSON handlers; handler/web holds the web UI handlers and middleware
internal/service      business rules and validation
internal/repository   GORM and in-memory implementations
internal/chat         WebSocket hub
internal/webhook      signed user.created webhook
web/                  embedded templates and static assets (HTMX vendored)
migrations/           SQL migrations
functions/notifier    AWS Lambda (separate Go module)
infra/                VM, Terraform, Floci and K3s deployment
```

## Development

```sh
VARIANT=movies go test -race -count=1 ./...   # no database needed
make test-integration                        # PostgreSQL repository tests (needs a *_test DB)
go vet ./... && make fmt
make swagger                                 # regenerate docs/ after changing handler annotations
make verify-isolation                        # build the binary and run it from an empty directory
```

`VARIANT` selects the expected behavior for the course contract tests in `internal/app`. CI sets it to
`movies`.

## Deployment

`infra/` deploys the app to a local lab: K3s on a Vagrant VM, plus Floci, a local AWS emulator, running the
Lambda and SES. See [infra/README.md](infra/README.md).

## License

MIT, see [LICENSE](LICENSE).
