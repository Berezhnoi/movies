# Plan: Movie trailers (Floci S3)
Tracking issue: #16
PRD: docs/prd/movie-trailers.md   Status: approved   Date: 2026-11-08

## 1. Summary
The work starts with a spike against Floci S3. After that comes a vertical API slice: a trailer table, a `TrailerStorage` interface in `service` with an in-memory fake, a `TrailerService`, and handlers, giving upload then stream end to end through `NewRouterWithDeps`.
The S3 implementation (AWS SDK v2 with static creds, `BaseEndpoint`, path-style, and a guard against real AWS) plugs in behind that interface, and `cmd/api` wires it only when `TRAILERS_ENABLED=true`.
`NewRouter()` gets no trailer routes, so it and the existing tests are untouched.
The web UI comes last: an upload/delete section on the edit form, the catalog link, and the watch page.
CI tests use the in-memory fake. Floci tests are skipped unless `TEST_S3_ENDPOINT` is set.

## 2. Baseline
- `go build ./...` and `go vet ./...` are green.
- `go test -race ./...` passes in all packages except `TestChatBroadcastsOnlyWithinRoom` (internal/app) and `TestAdminWebRegistrationSendsSignedEvent` (internal/webhook). Both panic on `httptest` port binding, which the local sandbox blocks (`bind: operation not permitted`). These are environment issues, not code failures; re-run outside the sandbox to confirm.
- `.env.example` exists, but the sandbox blocks reading it, so T8 has to update it.
- The test command is `go test -race ./...`. CI runs gofmt, `go vet` and `go test -race` (180s timeout).
- `go.mod` declares `go 1.21`, `ci.yml` still uses Go 1.22, and the Dockerfile uses Go 1.26. **The current AWS SDK for Go v2 needs Go ≥ 1.23** (N-1).

## 3. Decisions
| # | Decision | Alternative | Why |
|---|----------|-------------|-----|
| D1 | Trailer routes and the web UI exist only when `Deps.Trailers` is set. `NewRouter()` leaves it nil. | Register them always, with a fake | `NewRouter()` and the existing tests stay untouched. With the flag off, the routes return 404 (PRD assumption). |
| D2 | The `TrailerStorage` interface lives in `internal/service`. The S3 implementation and the in-memory fake live in a new `internal/storage`. | Put the interface in `repository` | The PRD and the user asked for it in the service layer. The fake makes FR-7/11/12 testable without Floci. |
| D3 | Each movie has a fixed object key, `trailers/<movie_id>.mp4`. | A UUID per upload | A replace is then one atomic overwrite, with no orphaned old objects (FR-2). |
| D4 | The upload is streamed through `MultipartReader` into a temp file, which checks the size limit and the `ftyp` magic bytes. The file is then sent with `PutObject`, which gets a known length and a seekable body. | SDK multipart `manager.Uploader` | This never buffers 50 MiB in memory. It also avoids S3 multipart APIs that Floci may not support and gives deterministic validation before any S3 call. |
| D5 | The service (`service.ParseByteRange`, called from `TrailerService.Open`) parses a single `bytes=` range and validates it against the HEAD size; an unsatisfiable range returns `RangeNotSatisfiableError` (416 `range_not_satisfiable`, with the handler setting `Content-Range: bytes */N`). Only that one resolved range is passed to storage `Get`, and the handler copies `Content-Length`/`Content-Range` from the storage response. A multi-range or malformed header gets 200 with the full body. | Parse in the handler, or `http.ServeContent` over a seeking S3 reader | Handlers only map typed errors (project pattern), the parser is unit-testable, and it does not depend on Floci's own 416 behaviour (Floci answers multi-range with 416). |
| D6 | (N-4) The precedence is 401/403 → 400 → 404 → 413/415/422. Write routes go on the existing `staff` group through a new `handler.RegisterTrailerAPI(public, staff, …)`, and auth runs first. The handler parses the id, checks the movie, then the body. `RegisterAPI` is unchanged. | A separate group with an id check before auth | This matches how the existing movie routes behave, and adds no extra group or middleware. |
| D7 | `RequestBodyLimit` skips the trailer upload routes, matched by `c.FullPath()`. The upload handler applies its own limit: 50 MiB for the file part, plus a small allowance for the multipart envelope. | Raise the global cap | The global 1 MiB cap stays everywhere else (NFR). |
| D8 | Movie delete cleanup goes through an additive `service.NewMovieServiceWithTrailerCleanup(repo, cleaner)`. After the movie is deleted it deletes the trailer row (needed for in-memory; GORM cascades anyway) and the object, and only logs failures. | Rely on the DB cascade | The in-memory repository has no FK, and the object still has to be removed (FR-10). |
| D9 | The S3 client is built with `s3.New(s3.Options{…})`, `aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(...))`, `BaseEndpoint`, and `UsePathStyle: true`. It does **not** use `config.LoadDefaultConfig`. | `LoadDefaultConfig` | The client never picks up `~/.aws`, IMDS, or `AWS_ENDPOINT_URL`, which belongs to the notifier. This is part of the real-AWS guard (FR-13). |
| D10 | The bucket is ensured at startup. If a `PutObject` fails with `NoSuchBucket`, the app runs `CreateBucket` and retries once. A missing bucket on read maps to `trailer_unavailable`. | Ensure at startup only | Floci loses the bucket on restart (FR-11, FR-7). |
| D11 | Compose wiring uses an override file, `docker-compose.floci.yml`, which joins the backend (and app1/app2) to the external `floci` network. | Add the network to `docker-compose.yml` | An external network missing from the default stack would make plain `docker compose up` fail. |
| D12 | In K3s, the ConfigMap gets `S3_ENDPOINT_URL=http://floci:4566` (resolved by the CoreDNS `floci` zone; confirmed in T1). The dummy keys go into the ConfigMap. | Put the keys in the `movies-secrets` Secret | They are dummy values Floci ignores, so a ConfigMap keeps it simple. |

## 4. Impact
- **New:**
  - `migrations/000004_add_movie_trailers.{up,down}.sql`
  - `internal/model/trailer.go`
  - `internal/repository/trailer*.go` (interface, in-memory, GORM)
  - `internal/service/trailer_service.go`, which holds the `TrailerStorage` interface and the errors
  - `internal/storage/{s3.go,memory.go}`
  - `internal/handler/trailer.go`
  - `internal/handler/web/trailer.go`
  - `web/templates/pages/trailer.html`, `web/templates/partials/trailer_section.html`
  - `docker-compose.floci.yml`, `.env.example`, `docs/floci-notes.md`
  - tests listed per task
- **Changed:**
  - `internal/app/app.go`: a `Trailers` dep and trailer route registration
  - `internal/apperr/errors.go`: 4 new codes
  - `internal/middleware/http.go`: body-limit exemption
  - `internal/config/config.go`: the trailer and S3 vars and the guard
  - `cmd/api/main.go`: wiring. It keeps `ReadHeaderTimeout` only, with no `ReadTimeout`/`WriteTimeout` (NFR)
  - `internal/service/movie.go`: the additive constructor
  - `internal/handler/web/{catalog.go,viewmodel.go}`, `web/templates/partials/movie_row.html`, `web/templates/pages/movie_form.html`
  - `infra/k8s/config.yaml`, `go.mod`/`go.sum`, `README.md`, `docs/` (swagger)
- **Env:** `TRAILERS_ENABLED`, `S3_ENDPOINT_URL`, `S3_BUCKET`, `AWS_REGION`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`. `TEST_S3_ENDPOINT` is used only by tests.
- **Unchanged:**
  - the Movie JSON and the `/api/v1/movies*` routes and status codes
  - `RegisterAPI`, `NewRouter()`, and the existing tests
  - the CSP header and the existing error codes

## 5. Tasks

### T1: Floci S3 spike   (covers R-1, FR-6, FR-7, FR-11)   depends on: none
- Goal: verify Floci's S3 behaviour before writing any code that depends on it, and check that the SDK fits the Go toolchain.
- Files: `docs/floci-notes.md` (new). A throwaway Go module outside the repo root (in the scratchpad), not committed.
- Steps:
  1. Run `make -C infra floci-up`.
  2. From the throwaway module, using the SDK as in D9 (`http://localhost:4566`, path-style, dummy keys), run CreateBucket, Put, Head, Get, and Delete.
  3. Send `Range: bytes=0-99` and record whether it returns 206 and `Content-Range`. Also try an out-of-range request.
  4. Try a key that does not exist and a bucket that does not exist, and record the HTTP status and error code for Get, Head and Delete.
  5. Restart Floci and record whether the bucket and objects survive (the `floci_data` volume).
  6. From a K3s pod, check `curl http://floci:4566` (Q-2).
  7. N-1: find the newest versions of `aws-sdk-go-v2`, `credentials` and `service/s3` (with their transitive SDK modules) whose `go.mod` declares `go` ≤ 1.22. Pin them in the throwaway module.
- Tests: none. This is a spike.
- Verify: `docs/floci-notes.md` has a pass/fail table for each item, plus a "PRD impact" section. If Range is not supported, FR-6 falls back to a full 200 response; if the restart keeps data, FR-7 still holds but rarely triggers.
- N-1 acceptance checks (all must pass, otherwise N-1 goes back to the user):
  - in the throwaway module with `go 1.22` in go.mod, `go mod tidy` leaves the `go` directive at ≤ 1.22 and adds no `toolchain` line
  - `GOTOOLCHAIN=go1.22.12 go build ./... && GOTOOLCHAIN=go1.22.12 go vet ./...` succeeds
  - the spike's S3 calls work with those pinned versions
  - the pinned versions are recorded in `floci-notes.md`, with any known S3 fixes they miss

### T2: Trailer data, storage interface, upload + stream API slice   (covers FR-1, FR-3, FR-4, FR-5, FR-6)   depends on: T1   **Status: done**
- Goal: an end-to-end slice through `NewRouterWithDeps` with the in-memory storage fake. Upload returns 201, and stream returns 200/206.
- Files:
  - `migrations/000004_*`, `internal/model/trailer.go`
  - `internal/repository/trailer.go` (interface + in-memory), `internal/service/trailer_service.go`
  - `internal/storage/memory.go`, `internal/handler/trailer.go`
  - `internal/apperr/errors.go`, `internal/middleware/http.go`, `internal/app/app.go`
- Steps:
  1. Migration `movie_trailers`: `movie_id` BIGINT PK FK → movies ON DELETE CASCADE, `object_key`, `content_type`, `size_bytes`, `uploaded_by` FK → users ON DELETE SET NULL, `uploaded_at`.
  2. Service: `Upload`, `Metadata` (with HEAD), and `Open` (with range). Errors: `TrailerUnavailableError`, `ErrStorageUnavailable`, `PayloadTooLargeError`, `UnsupportedMediaTypeError`, mapped in apperr to 404/503/413/415.
  3. Handler: implement D4, D6 and D7, and stream the range resolved by the service (D5), `Content-Type: video/mp4`, and `X-Content-Type-Options: nosniff`.
  4. Add `Deps.Trailers` (D1).
- Tests: `internal/app/trailer_test.go`, using the `rbac_test.go` helpers:
  - upload 201 → metadata 200 → full GET 200 with identical bytes, plus the headers
  - `bytes=0-99` → 206 with the correct `Content-Range`, an unsatisfiable range → 416, a multi-range request → 200
  - over 50 MiB by Content-Length → 413, and over the read limit with a lying Content-Length → 413; a non-MP4 with a `video/mp4` header → 415; an empty file → 422; an unknown movie → 404; `abc` → 400
  - the precedence table (N-4): a bad id with no token → 401; a bad id with a viewer → 403; a bad id with an editor → 400; an unknown movie with an editor and an oversized or non-MP4 body → 404
  - with `NewRouter()`, `/api/v1/movies/1/trailer` → 404
- Verify: `go test -race -count=1 ./internal/app -run Trailer` passes, and the full suite stays green.

### T3: Replace, delete, lost object, storage down, movie-delete cleanup (#8)   (covers FR-2, FR-7, FR-8, FR-9, FR-10, FR-11, FR-12)   depends on: T2 **Status: done**
- Goal: complete the lifecycle and failure behaviour using the fake's fault switches (drop object, drop bucket, unavailable).
- Files:
  - `internal/service/trailer_service.go`, `internal/service/movie.go` (D8)
  - `internal/handler/trailer.go`, `internal/storage/memory.go`
  - `internal/app/app.go`, `internal/app/trailer_test.go`
  - `internal/service/trailer_service_test.go`
- Steps:
  1. Replace returns 200 using the same key. A DB failure after the put returns 500 and logs.
  2. Delete returns 204 even when the object is missing, and 503 (keeping the row) when storage is unavailable.
  3. A missing object or bucket on read → 404 `trailer_unavailable`, the row is kept, and a warning is logged.
  4. Add the movie-delete cleaner.
- Tests:
  - replace 200 returns the new bytes, with one row
  - drop the object → metadata and stream return 404 `trailer_unavailable`, the row is still there, and a re-upload works
  - drop the bucket → the same, and the next upload returns 201 (the fake records `CreateBucket`)
  - unavailable → upload, metadata, stream and delete return 503, the row is kept, and `/api/v1/movies` still returns 200
  - admin movie delete → the trailer row and object are gone, and a forced object-delete failure still returns 204
  - a service unit test where the repo fails after the put → 500
- Verify: `go test -race -count=1 ./internal/app ./internal/service -run 'Trailer|Movie'` passes.

### T4: GORM trailer repository (#9)   (covers FR-1, FR-2, FR-9, FR-10)   depends on: T2   parallel with: T3
- Goal: a GORM repository that behaves like the in-memory one.
- Files: `internal/repository/trailer_gorm.go`, `internal/repository/trailer_gorm_test.go`, and an addition to `integration_test.go`.
- Steps:
  1. Implement Get, Upsert (on conflict `movie_id`), Delete, and `ExistsFor(ids)` (used by T7).
  2. Map not-found errors the way `movie_gorm.go` does.
- Tests: a shared behaviour table run against both implementations. Against Postgres (`TEST_DATABASE_URL`): the cascade on movie delete.
- Verify: `go test -race ./...` is green, and `make test-integration` passes against `_test` DB.

### T5: S3 storage, config and real-AWS guard (#10)   (covers FR-11, FR-12, FR-13, NFR security)   depends on: T1, T2   parallel with: T3, T4
- Goal: a real implementation of `TrailerStorage`, wired in `cmd/api` when `TRAILERS_ENABLED=true`.
- Files: `go.mod`/`go.sum`, `internal/storage/s3.go`, `internal/storage/s3_floci_test.go`, `internal/config/config.go`, `internal/config/config_test.go`, `cmd/api/main.go`.
- Steps:
  1. Add the SDK pinned to the T1 versions (`aws-sdk-go-v2 v1.39.2`, `service/s3 v1.88.4`, `credentials v1.18.16`, `smithy-go v1.23.0`), with `go 1.22` in go.mod. Build the client as in D9.
  2. Map errors: `NoSuchKey`, `NoSuchBucket`, `NotFound`, and HEAD 404 → object-not-found; network or 5xx → unavailable (based on the T1 findings).
  3. Ensure the bucket and retry once on put (D10).
  4. In config, `TRAILERS_ENABLED` is a strict bool. When it is true, `S3_ENDPOINT_URL` (parsed, http/https) and `S3_BUCKET` are required, and the app rejects an empty endpoint or a host equal to or ending in `.amazonaws.com` (also `amazonaws.com.cn`).
  5. Leave the server timeouts as they are (no `ReadTimeout`/`WriteTimeout`).
- Tests:
  - config: disabled and empty → OK; enabled and empty → error; `s3.amazonaws.com` → error; `bucket.s3.eu-west-1.amazonaws.com` → error; `http://floci:4566` → OK
  - Floci tests, skipped unless `TEST_S3_ENDPOINT` is set: put/head/get, a range, missing key, missing bucket, and auto-create after the bucket is deleted
- Verify: `go test -race ./...` is green without Floci. `TEST_S3_ENDPOINT=http://localhost:4566 go test -race ./internal/storage` passes against Floci.
- Manual check against real Floci (at the end of T5, recorded in `floci-notes.md`):
  1. `make -C infra floci-up`, then `make run` with `TRAILERS_ENABLED=true` and `S3_ENDPOINT_URL=http://localhost:4566`.
  2. Upload a small MP4 with an editor token → 201. Full GET → 200 with the same bytes. `Range: bytes=0-99` → 206.
  3. Restart Floci. Metadata and stream → 404 `trailer_unavailable`. Re-upload → 201, since the bucket is recreated.
  4. Stop Floci. The trailer endpoints → 503 and `/api/v1/movies` → 200.
  5. Start the app with `S3_ENDPOINT_URL=https://s3.amazonaws.com` → it refuses to start.

### T6: Compose and K3s wiring (#11)   (covers FR-12, PRD §7 Floci location)   depends on: T5
- Goal: the app reaches Floci in both environments.
- Files: `docker-compose.floci.yml` (new), `infra/k8s/config.yaml`, `infra/README.md`, and `infra/k3s-smoke.sh` if a smoke check fits there.
- Steps:
  1. The override file attaches backend/app1/app2 to the external `floci` network and sets `TRAILERS_ENABLED=true` and `S3_ENDPOINT_URL=http://floci:4566` (D11).
  2. Add the S3 keys to the ConfigMap (D12), using the hostname confirmed in T1.
- Tests: manual smoke. Upload a small MP4 via curl, stream it with a Range, restart Floci, and check that the response is 404 `trailer_unavailable`.
- Verify: `make -C infra floci-up && docker compose -f docker-compose.yml -f docker-compose.floci.yml up -d --build`, then `curl -I .../api/v1/movies/1/trailer/video` returns 200. In K3s, `kubectl -n movies rollout status deploy/...` succeeds and the same curl through Traefik returns 200.

### T7: Web UI — edit form section, catalog link, watch page (#12)   (covers FR-7 UI, FR-14)   depends on: T3, T4
- Goal: the minimal web UI under the existing CSP and CSRF rules.
- Files:
  - `internal/handler/web/trailer.go`, `internal/handler/web/catalog.go`, `internal/handler/web/viewmodel.go`
  - `web/templates/pages/movie_form.html`, `web/templates/partials/trailer_section.html`, `web/templates/partials/movie_row.html`, `web/templates/pages/trailer.html`
  - `internal/app/app.go`, and `web/static/css` if needed
- Steps:
  1. **Edit form:** staff get a trailer section with an `hx-post` (`hx-encoding="multipart/form-data"`) to `POST /movies/:id/trailer`. `app.js` already sends `X-CSRF-Token`, and that route is added to the D7 exemption. Delete uses `hx-delete /web/movies/:id/trailer`, and both swap the section fragment.
  2. **Catalog link:** the row view model gets `HasTrailer`, filled from one `ExistsFor(ids)` query per page with no storage calls.
  3. **Watch page:** `GET /movies/:id/trailer` (OptionalAuth) renders `<video controls src="/api/v1/movies/:id/trailer/video">`, or "Trailer unavailable" when the service returns `trailer_unavailable`.
  4. Use no inline script or style, and no trusted types.
- Tests: `internal/app/web_trailer_test.go`:
  - staff see the controls and a viewer or anonymous user does not; an upload with CSRF and Origin succeeds, and one without them → 403
  - the link shows only when a row exists, and the catalog renders with storage unavailable (no storage call)
  - the watch page in its normal, unavailable and none states
  - the CSP header is unchanged
  - `audit_test.go` still passes
- Verify: `go test -race -count=1 ./internal/app ./internal/handler/web` passes. A manual check in the browser via `make run` with Floci shows no CSP console errors and seeking works.

### T8: Docs — README, .env.example, Swagger (#13)   (covers DoD)   depends on: T5, T6, T7
- Files: `README.md`, `.env.example` (new), `docs/` (generated), and the handler swag annotations.
- Steps:
  1. Fix README.md:57 to say the catalog is public.
  2. Add a "Trailers" section: routes, error codes, env vars, and how to run with Floci in compose and K3s, linking `docs/floci-notes.md`.
  3. Add the new variables to `.env.example` with dummy values only. The sandbox blocks reading this file, so ask the user to allow it or to apply the snippet.
  4. Run `make swagger`.
- Verify: `make swagger` produces a clean diff limited to the trailer paths. `go test -race ./...` passes.

### T9: Full verification (#14)   (covers all NFR compatibility)   depends on: T8
- Steps:
  1. Run `gofmt -l .` (empty), `go vet ./...`, and `go test -race -timeout 180s ./...` outside the sandbox.
  2. Run `make build && make verify-isolation`.
  3. Run the Floci-gated tests with `TEST_S3_ENDPOINT`.
- Verify: all of the above are green.

### T10: Review with go-review-best-practices (#15)   depends on: T9
- Steps:
  1. Run the `go-review-best-practices` skill on the branch diff, with attention to layering, ctx propagation, temp-file cleanup, closing S3 response bodies, error mapping, and that no AWS creds reach the logs.
  2. Fix the findings and re-run T9.
- Verify: no unresolved high or medium findings.

## 6. Traceability
| Requirement | Task(s) | Test(s) |
|---|---|---|
| FR-1 upload | T2, T4 (#9) | trailer_test upload 201; trailer_gorm_test |
| FR-2 replace / atomic | T3 (#8), T4 (#9) | trailer_test replace; trailer_service_test DB-fail → 500 |
| FR-3 validation + precedence (N-4) | T2 | trailer_test 413/415/422/404/400 and 401/403 → 400 → 404 → body table |
| FR-4 auth | T2 | trailer_test 401/403/editor/admin, public reads |
| FR-5 metadata + HEAD | T2, T3 (#8) | trailer_test metadata; dropped object → 404 |
| FR-6 stream/range/headers | T1, T2, T5 (#10) | trailer_test 200/206/416/multi-range; s3_floci_test range |
| FR-7 lost object / bucket | T1, T3 (#8), T7 (#12) | trailer_test drop object/bucket; web_trailer_test unavailable |
| FR-8 stale cleanup | T3 (#8) | trailer_test delete 204 with missing object, re-upload |
| FR-9 delete | T3 (#8), T4 (#9) | trailer_test delete 204 → 404 |
| FR-10 movie delete | T3 (#8), T4 (#9) | trailer_test admin delete; integration cascade |
| FR-11 bucket recovery | T3 (#8), T5 (#10) | trailer_test fake CreateBucket; s3_floci_test auto-create |
| FR-12 storage down (incl. delete) | T3 (#8), T5 (#10), T6 (#11) | trailer_test unavailable → 503 for upload, metadata, stream and delete with the row kept, movies unaffected; T5 manual check |
| FR-13 real-AWS guard (only when enabled) | T5 (#10) | config_test: disabled + empty → OK, enabled guard cases; T5 manual check |
| FR-14 web UI | T7 (#12) | web_trailer_test |
| NFR compatibility (Movie contract, NewRouter, existing tests) | T2, T9 (#14) | existing movie tests pass unchanged; NewRouter trailer 404 test |
| NFR body cap: 1 MiB elsewhere, 50 MiB on API + web upload routes | T2, T7 (#12) | 413 tests; new test that `/api/v1/movies` POST > 1 MiB → 413; web upload test |
| NFR CSRF/Origin on the web upload | T7 (#12) | web_trailer_test without token or Origin → 403 |
| NFR timeouts do not cut uploads or streams | T5 (#10), T9 (#14) | main.go keeps only `ReadHeaderTimeout` (review); T5 manual check uploads and streams a near-50 MiB file |
| NFR security: dummy creds, keys from movie id, CSP `'self'`, nosniff, error JSON | T2, T5 (#10), T7 (#12) | header asserts; object key assert in fake; config_test; CSP assert; error-shape asserts |
| NFR testability: no Floci in CI, opt-in Floci tests | T5 (#10), T9 (#14) | `go test -race ./...` without Floci; `TEST_S3_ENDPOINT` skip |
| NFR performance: no full-file buffering | T2, T10 (#15) | temp-file upload and `io.Copy` stream; review checklist |
| NFR observability: logs for upload, delete, lost object and storage errors | T3 (#8) | log capture (`log.SetOutput` to a buffer) asserts movie id in the lost-object and storage-error logs |
| NFR simplicity: one bucket, one object per movie, no background jobs | T2, T10 (#15) | fixed-key assert; review |

## 7. Risks and rollback
- **Floci gaps (Range, error codes):** found in T1. The fallbacks are noted in `floci-notes.md`, and the PRD is amended before T2.
- **An SDK upgrade past the T1 pins needs Go ≥ 1.23 and would break CI on Go 1.22:** noticed when CI fails at `go mod download`. Keep the pins until CI moves to a newer Go.
- **The body-limit exemption is too broad:** caught by the existing 1 MiB tests and a new test that `/api/v1/movies` still returns 413 above 1 MiB.
- **Known limitation: concurrent uploads to the same movie** (T2 review #1): both write the fixed key, so the row's `size_bytes` and `uploaded_at` can describe the other upload. Streaming is unaffected, because sizes and ranges come from the storage HEAD and GET responses. Accepted for this project; per-movie serialization (a lock, or a row lock in Postgres) would fix it if needed.
- **Temp files leak on errors:** covered by the T10 review and a deferred remove.
- **Rollback in general:** set `TRAILERS_ENABLED=false`, which removes the routes and UI. The migration down drops `movie_trailers`. The movie data is never touched.

## 8. Needs decision
- **N-1 (resolved by T1): Go version.** CI uses Go 1.22 (ci.yml line 15), and the current AWS SDK v2 needs Go ≥ 1.23. T5 pins `aws-sdk-go-v2 v1.39.2`, `service/s3 v1.88.4`, `credentials v1.18.16` and `smithy-go v1.23.0`, all of which declare `go 1.22` and passed the T1 checks (see `docs/floci-notes.md`). The `go` directive in `go.mod` becomes 1.22.
- **N-4 (resolved):** the precedence is 401/403 → 400 → 404 → 413/415/422, using the existing staff group with no separate route group (D6).
- **N-2 (resolved): Web upload without JavaScript.** The CSRF check reads the token from the header or from `ParseForm`, which does not parse multipart bodies. Decision: the web upload works only through HTMX (which adds the `X-CSRF-Token` header), as the rest of the UI already does. No no-JS fallback.
- **N-3 (resolved): The response to a web trailer delete.** Decision: return the refreshed trailer-section fragment with 200, instead of the empty 200 used by movie row deletes, so the upload control reappears.

## 9. Definition of done
- [ ] T1 findings are recorded in `docs/floci-notes.md`, and the PRD is amended if needed
- [ ] All FR acceptance criteria pass, and the Floci-gated tests cover FR-1, FR-6, FR-7 and FR-11
- [ ] `gofmt`, `go vet` and `go test -race ./...` are green without Floci
- [ ] Swagger is regenerated, and the README has the trailers section and the fixed line 57
- [ ] `.env.example` is updated with the new variables
- [ ] The Movie contract and `NewRouter()` are unchanged, and the existing tests pass
- [ ] The go-review-best-practices review is done and its findings are resolved
