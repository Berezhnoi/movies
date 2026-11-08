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

Prerequisites: Go 1.26+, Docker Compose, `make`.

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
| `GET` | `/api/v1/movies/{id}/trailer`, `/api/v1/movies/{id}/trailer/video` | Public (only with `TRAILERS_ENABLED=true`) |
| `PUT`, `DELETE` | `/api/v1/movies/{id}/trailer` | Editor, admin (only with `TRAILERS_ENABLED=true`) |

## Web UI

| Page | Route | Access |
|---|---|---|
| Login, registration | `/login`, `/register` | Public (admins can also register users) |
| Catalog with live search | `/`, `/catalog` | Public |
| Create, edit movie | `/movies/new`, `/movies/{id}/edit` | Editor, admin |
| Delete movie (HTMX) | `DELETE /web/movies/{id}` | Admin |
| Movie chat | `/movies/{id}/chat` (socket at `/ws/movies/{id}`) | Logged in |
| Watch trailer | `/movies/{id}/trailer` | Public |
| Upload, delete trailer (HTMX, on the edit form) | `POST /movies/{id}/trailer`, `DELETE /web/movies/{id}/trailer` | Editor, admin |

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
| `REDIS_URL` | empty | Empty keeps the chat in memory; set it to share the chat via Redis |
| `CHAT_HISTORY_SIZE` | `50` | Messages kept per room when Redis is on |
| `TRAILERS_ENABLED` | `false` | `true` turns on the trailer routes and UI; see [Movie trailers](#movie-trailers) |

## Movie chat

Without `REDIS_URL` the chat lives in one process's memory: one instance, no history. With `REDIS_URL` each
message is published to the Redis channel `chat:movie:{id}`, so every instance delivers it to its own clients,
and the last `CHAT_HISTORY_SIZE` messages (list `chat:history:movie:{id}`) are sent to each user who joins.
The app refuses to start if Redis does not answer; later Redis errors are logged and the message is delivered
locally only. Try it with two instances (ports 8081 and 8082):

```sh
docker compose --profile scale up --build   # plain `docker compose up` also starts redis, but not app1/app2
# Open the same movie's chat at http://localhost:8081 and http://localhost:8082 and log in on each tab.
docker compose exec redis redis-cli LRANGE chat:history:movie:1 0 -1
docker compose exec redis redis-cli PUBSUB NUMPAT   # 1: instances use PSUBSCRIBE, so PUBSUB CHANNELS is empty
TEST_REDIS_URL=redis://127.0.0.1:6379 go test -race ./internal/app -run Redis
```

The history is capped and is not durable storage: Redis runs without persistence and loses it on restart.

## Movie trailers

Each movie can have one MP4 trailer (at most 50 MiB), stored in S3 on [Floci](infra/README.md), a local AWS
emulator. Browsers never talk to S3: uploads and streams go through the app. With `TRAILERS_ENABLED=false`
(the default) the trailer routes return 404, the UI shows no trailer controls, and no S3 settings are read.

| Method | Endpoint | Access | Result |
|---|---|---|---|
| `PUT` | `/api/v1/movies/{id}/trailer` (multipart field `file`) | Editor, admin | 201 created, 200 replaced |
| `GET` | `/api/v1/movies/{id}/trailer` | Public | Metadata (checks the object exists in S3) |
| `GET` | `/api/v1/movies/{id}/trailer/video` | Public | 200 full body, 206 for one `Range: bytes=` range |
| `DELETE` | `/api/v1/movies/{id}/trailer` | Editor, admin | 204 |

The file type is decided by the MP4 `ftyp` signature, not the `Content-Type` header. The object key is
`trailers/<movie id>.mp4`, never the client file name. The 50 MiB limit applies only to the two upload routes;
every other route keeps the 1 MiB body cap. Deleting a movie also removes its trailer (a failed S3 delete is
only logged). New error codes:

| Status | Code | When |
|---|---|---|
| 404 | `trailer_unavailable` | The trailer row exists but the object (or the bucket) is gone from S3 |
| 413 | `payload_too_large` | The file is over 50 MiB |
| 415 | `unsupported_media_type` | The file is not an MP4 |
| 416 | `range_not_satisfiable` | The `Range` is outside the file (`Content-Range: bytes */<size>`) |
| 503 | `storage_unavailable` | S3 cannot be reached; a trailer delete keeps the row |

An empty or missing file returns 422 `validation_error`. Checks run in the order auth (401/403), id (400),
movie (404), then the file (413/415/422).

Settings (read only when `TRAILERS_ENABLED=true`):

| Variable | Default | Notes |
|---|---|---|
| `S3_ENDPOINT_URL` | — | Required, `http` or `https`. The app refuses to start if it is empty or its host is under `amazonaws.com` |
| `S3_BUCKET` | — | Required. Created at startup and again on upload if Floci lost it |
| `AWS_REGION` | `us-east-1` | |
| `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` | `test` | Dummy values for Floci only; never put real AWS keys here |
| `TEST_S3_ENDPOINT` | empty | Tests only: runs the Floci storage tests (skipped when empty) |

The S3 client always uses path-style addressing and the static keys above; it never reads `~/.aws`,
instance metadata or `AWS_ENDPOINT_URL`.

Run it locally with Floci (`make run` on the host, or the containers with the second compose file):

```sh
make -C infra floci-up                                                    # Floci on localhost:4566
make run TRAILERS_ENABLED=true S3_ENDPOINT_URL=http://localhost:4566 S3_BUCKET=trailers
# or in Docker Compose (joins the external floci network, S3_ENDPOINT_URL=http://floci:4566):
docker compose -f docker-compose.yml -f docker-compose.floci.yml up -d --build   # add --profile scale for app1/app2
TEST_S3_ENDPOINT=http://localhost:4566 go test -race ./internal/storage
```

Smoke check (editor or admin token in `$TOKEN`):

```sh
curl -s -X PUT -H "Authorization: Bearer $TOKEN" -F "file=@small.mp4;type=video/mp4" \
  http://localhost:8080/api/v1/movies/1/trailer                                     # 201 or 200
curl -s -o /dev/null -w '%{http_code}\n' -H 'Range: bytes=0-99' \
  http://localhost:8080/api/v1/movies/1/trailer/video                               # 206
```

**Lost objects.** Floci keeps S3 data in memory, so a Floci restart drops the bucket and every trailer while
the rows stay in PostgreSQL. After that, metadata and stream return 404 `trailer_unavailable`, the watch page
shows "Trailer unavailable", and the catalog still shows the "Trailer" link (it checks only the row). An editor
fixes it by uploading the file again (the bucket is recreated without an app restart) or by deleting the
trailer, which succeeds even though the object is gone. There is no automatic cleanup.

**K3s (not verified).** `infra/k8s/config.yaml` sets the same variables with
`S3_ENDPOINT_URL=http://floci:4566`, which pods resolve through the CoreDNS `floci` zone
(`make -C infra k8s-dns`) to the VM, where Floci runs outside the cluster. `infra/k8s/app.yaml` adds a `/tmp`
volume for the upload temp file. Steps: `make -C infra floci-up`, `make -C infra k8s-dns`,
`make -C infra k8s-apply`, then the smoke check above against `http://localhost:30080`. These manifest changes
have not been run in a cluster yet; see [infra/README.md](infra/README.md#6-trailers-on-floci-s3).

Details of Floci's S3 behaviour (ranges, error codes, restarts): [docs/floci-notes.md](docs/floci-notes.md).

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
- Without `REDIS_URL`, chat rooms are kept in memory, so the chat works with a single replica only.
- Trailer streaming is public and has no rate limiting. Floci does not check request signatures, so anything
  that can reach port 4566 can read or overwrite trailers.

## Project layout

```
cmd/api, cmd/seed     server and first-admin seed command
internal/app          router wiring (API, auth groups, web UI, Swagger)
internal/handler      JSON handlers; handler/web holds the web UI handlers and middleware
internal/service      business rules and validation
internal/repository   GORM and in-memory implementations
internal/chat         WebSocket hub, optional Redis broker
internal/webhook      signed user.created webhook
internal/storage      trailer storage: S3 (Floci) and in-memory
web/                  embedded templates and static assets (HTMX vendored)
migrations/           SQL migrations
functions/notifier    AWS Lambda (separate Go module)
infra/                VM, Terraform, Floci and K3s deployment
```

## Development

```sh
go test -race -count=1 ./...                  # no database needed
make test-integration                        # PostgreSQL repository tests (needs a *_test DB)
TEST_REDIS_URL=redis://127.0.0.1:6379 go test -race ./internal/app -run Redis   # chat over Redis
go vet ./... && make fmt
make swagger                                 # regenerate docs/ after changing handler annotations
make verify-isolation                        # build the binary and run it from an empty directory
```

### Test database

`make test-integration` truncates tables, so it needs its own database next to the dev one, in the same
Postgres container:

- `DB_NAME` is the dev database (for example `movies`). Don't give it a `_test` suffix.
- `TEST_DB_NAME` must end in `_test` and differ from `DB_NAME` (for example `movies_test`). `make test-db`
  and the tests refuse to run otherwise.
- `TEST_DATABASE_URL` (host, `127.0.0.1:$DB_PORT`) and `TEST_DATABASE_DOCKER_URL` (compose network, `db:5432`)
  must both name `TEST_DB_NAME`.

`make test-db` creates `TEST_DB_NAME` if it is missing, so `DB_USER` needs the `CREATEDB` right. On a fresh
volume `DB_USER` is the container's superuser and already has it. If the volume was created with another
user, or the role was changed, grant it as a superuser:

```sh
docker compose exec db psql -U <superuser> -d postgres -c 'ALTER ROLE "<DB_USER>" CREATEDB'
```

Alternatively, recreate the volume (`docker compose down -v`, which deletes the dev data) or create the
database once by hand as a superuser.

## Deployment

`infra/` deploys the app to a local lab: K3s on a Vagrant VM, plus Floci, a local AWS emulator, running the
Lambda and SES. See [infra/README.md](infra/README.md).

## License

MIT, see [LICENSE](LICENSE).
