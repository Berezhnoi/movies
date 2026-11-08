# PRD: Movie trailers (Floci S3)
Status: approved   Date: 2026-11-08   Slug: movie-trailers

## 1. Problem and goal
The catalog only shows text, and there is no way to preview a movie. Staff want to attach a short trailer, and visitors want to watch it in the browser. The course lab already runs Floci (a local AWS emulator), so this is a chance to practise S3 with the real AWS SDK without touching real AWS. Floci keeps data in memory by default, so the feature must behave predictably when a file disappears but its database record stays.
Goal: an editor or admin can upload one MP4 trailer per movie, and anyone who can see the movie can stream it from the app.

## 2. Users and roles
- Anonymous / viewer: can see trailer metadata and stream the trailer. The catalog is public today, so trailers are public too.
- Editor / admin (staff): can do the same, and can also upload, replace and delete a movie's trailer.
- Floci S3: the storage backend. Browsers never reach it directly.

## 3. User stories
- As an editor, I want to upload a trailer on the movie edit form so that visitors can preview the movie.
- As an editor, I want to replace or delete a trailer so that I can fix a wrong file or remove a lost one.
- As a visitor, I want to click "Trailer" in the catalog and watch it with seeking so that I can decide whether to watch the movie.
- As an API client, I want trailer endpoints separate from the movie resource so that existing integrations keep working.

## 4. Scope
In scope: one trailer per movie. JSON API to upload/replace, get metadata, stream and delete. Streaming goes through the app with HTTP Range support. Minimal web UI: a staff upload/delete control on the edit form, a "Trailer" link in the catalog row and a small watch page. Floci wiring for docker compose and K3s. A guard against real AWS. Defined behaviour when the object is lost.
Out of scope (explicitly): multiple trailers or galleries, formats other than MP4, transcoding, thumbnails, HLS/DASH, presigned or direct-to-S3 browser URLs, resumable or chunked uploads, an automatic reconcile or cleanup job, Floci persistence configuration, and real AWS deployment.

## 5. Functional requirements
- FR-1 Upload. Staff upload an MP4 file for an existing movie. Acceptance: Given an editor token and a valid MP4 of 50 MiB or less, when they upload to a movie with no trailer, then the response is 201 with metadata (movie id, content type, size, upload time), and a later stream request returns the same bytes.
- FR-2 Replace. Uploading again replaces the trailer. Acceptance: Given a movie that has a trailer (stale or not), when staff upload a new file, then the response is 200, streaming returns the new file, and no second record exists. Each movie has a fixed object key, so a replace is a single atomic object overwrite. If the database update fails after the object is written → 500 `internal_server_error`, and the failure is logged with the movie id.
- FR-3 Validation. Acceptance: a file over 50 MiB → 413 `payload_too_large`; a file whose type or magic bytes are not MP4 → 415 `unsupported_media_type`; a missing or empty file → 422 `validation_error`; an unknown movie id → 404 `not_found`; a malformed id → 400 `invalid_id`. In each case nothing is stored. Checks run in this order: 401/403 auth, then 400 id, then 404 movie, then 413/415/422 file. Size is checked against Content-Length first and then by a hard read limit while the body is read. The type is decided by MP4 magic bytes (an `ftyp` box), never by the client's Content-Type header.
- FR-4 Auth. Acceptance: upload or delete without a token → 401 `unauthorized`; with a viewer token → 403 `forbidden`; with an editor or admin token → allowed. Metadata and stream need no token.
- FR-5 Metadata. Acceptance: Given a movie with a trailer, when GET metadata is called, then 200 with the FR-1 fields. With no trailer → 404 `not_found`. Metadata always checks that the object exists in storage (HEAD) before answering (see FR-7).
- FR-6 Stream. Acceptance: a full GET → 200, `Content-Type: video/mp4`, `Accept-Ranges: bytes`, full body. `Range: bytes=0-99` → 206 with the correct `Content-Range` and 100 bytes. A range that cannot be satisfied → 416. No trailer → 404 `not_found`. Every stream response sends `Content-Type: video/mp4` and `X-Content-Type-Options: nosniff`. `Content-Length` and `Content-Range` come from the storage response, not from the database row. Only a single byte range is supported: a multi-range request gets the full body (200).
- FR-7 Lost object. The database row exists but the object is gone from Floci, for example after a restart. Acceptance: metadata and stream both return 404 `trailer_unavailable` with a message telling staff to upload the trailer again. The row is kept. A missing bucket on read is treated the same way (404 `trailer_unavailable`). The web watch page shows "Trailer unavailable" instead of a broken player. A warning is logged with the movie id.
- FR-8 Cleanup of a stale record. Acceptance: Given FR-7's state, when staff delete the trailer, then the response is 204 and the row is removed even though the object is missing. When staff upload again (FR-2), the trailer works again.
- FR-9 Delete. Acceptance: staff delete → 204, after which metadata and stream return 404 `not_found`. Delete when no trailer exists → 404 `not_found`.
- FR-10 Movie deletion. Acceptance: when an admin deletes a movie, its trailer row is removed too, and the object deletion is attempted. A failed object deletion does not fail the movie delete; it is only logged.
- FR-11 Bucket recovery. A Floci restart also loses the bucket. Acceptance: after Floci restarts with an empty state, without restarting the app, the next upload succeeds (201). Reads before that upload follow FR-7.
- FR-12 Storage down. Acceptance: when Floci cannot be reached, upload, metadata, stream and trailer delete return 503 `storage_unavailable`. A delete that returns 503 leaves the row in place. Movie endpoints are unaffected, and movie delete stays log-only (FR-10).
- FR-13 Real-AWS guard. Acceptance: with `TRAILERS_ENABLED=true`, the app refuses to start, with a clear log message, when the S3 endpoint is empty or its host is under `amazonaws.com`. With `TRAILERS_ENABLED=false`, the app starts normally without any S3 configuration.
- FR-14 Web UI. Acceptance: a staff user on the edit form can upload (CSRF and Origin checks pass) and delete. A viewer or anonymous user sees no controls. A catalog row shows a "Trailer" link only when a trailer record exists. This check uses only the database row, with no storage call per row. The watch page plays it in `<video controls>` under the current CSP, with no inline script or style.

## 6. Non-functional requirements
- Compatibility: the Movie JSON shape, the `/api/v1/movies` routes and status codes, `NewRouter()` must not change, and the existing tests must keep passing. The global 1 MiB body cap stays for every route except trailer upload, whose limit is 50 MiB. The exemption covers whichever upload route the web form posts to, and that route still enforces CSRF and Origin checks. Server read and write timeouts must not cut off a 50 MiB upload or a full trailer stream.
- Security: no real AWS credentials, only dummy ones from env. Browsers only talk to the app (CSP stays `'self'`). Object keys come from the movie id and never from the client filename. Errors use the existing `{"error":{"code","message"}}` format.
- Testability: the full suite runs without Floci (CI). The Floci tests are opt-in, like the Redis tests.
- Performance: streaming must not load the whole file into memory. Uploads up to 50 MiB must not be buffered in memory all at once.
- Observability: log upload, delete, the lost-object case and storage errors with the movie id.
- Simplicity: one bucket, one object per movie, no background jobs.

## 7. Interfaces and data (sketch)
- `PUT /api/v1/movies/:id/trailer` (staff): multipart field `file` → 201/200 and metadata.
- `GET /api/v1/movies/:id/trailer` (public): metadata JSON.
- `GET /api/v1/movies/:id/trailer/video` (public): MP4 stream with Range support.
- `DELETE /api/v1/movies/:id/trailer` (staff): 204.
- Web: an upload/delete control on the edit form, a "Trailer" link in the catalog row, and a watch page.
- Table `movie_trailers`: movie_id (unique, FK with cascade on delete), object key, content type, size, uploaded_at, uploaded_by. Added in a new migration (000004).
- New error codes: `trailer_unavailable` (404), `storage_unavailable` (503), `payload_too_large` (413), `unsupported_media_type` (415).
- Env: `TRAILERS_ENABLED`, `S3_ENDPOINT_URL`, `S3_BUCKET`, `AWS_REGION`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`. Path-style addressing is always on.
- Where Floci runs:
  - docker compose: Floci is its own compose project (`infra/floci/docker-compose.yml`, hostname `floci`, port 4566), and the app container reaches it over a shared Docker network at `http://floci:4566`.
  - K3s: Floci runs on the VM outside the cluster, and pods reach it through the existing CoreDNS `*.floci` zone. The endpoint goes in `infra/k8s/config.yaml`.

## 8. Risks and dependencies
- R-1 (spike first): Floci's S3 support has not been verified. The spike checks path-style CreateBucket, Put, Get, Head and Delete, a Range GET returning 206 with `Content-Range`, the error returned for a missing key or bucket, and what is left after a restart. If Range is not supported, the fallback is a full 200 response with no seeking, and FR-6 is updated.
- R-2: data loss on a Floci restart is by design (FR-7, FR-8, FR-11). Rows and objects can drift apart.
- R-3: the 50 MiB upload exemption adds risk of large-body DoS. It is limited to the staff-only route.
- R-4: networking between the separate compose projects and the K3s → VM DNS name has not been verified.
- R-5 (accepted): public streaming has no rate limiting.
- Dependency: AWS SDK for Go v2 (S3).

## 9. Open questions and assumptions
- Q-1 (resolved): use a separate `S3_ENDPOINT_URL`, not `AWS_ENDPOINT_URL`.
- Q-2: the exact K3s hostname for Floci S3 in the `*.floci` zone. (owner: spike, blocking: no)
- ASSUMPTION: trailers are public because the catalog is public in code. README.md:57 says the catalog is "Logged in", which contradicts the code, and should be corrected. (blocking: no)
- ASSUMPTION: with `TRAILERS_ENABLED=false`, the trailer routes are absent (404) and the UI shows no trailer controls.
- ASSUMPTION: the root `docker-compose.yml` joins the Floci network instead of embedding Floci.

## 10. Definition of done
- [ ] All FR acceptance criteria pass. The Floci integration test covers FR-1, FR-6, FR-7 and FR-11.
- [ ] Spike R-1 is done and its findings are recorded before implementation.
- [ ] `go test -race ./...`, `go vet` and gofmt are green with no Floci running.
- [ ] Swagger is regenerated, and the README documents the routes, env vars and Floci setup for compose and K3s.
- [ ] `.env.example` is updated with the new variables, including the `TRAILERS_ENABLED` and S3 settings.
- [ ] README.md:57 is fixed to say the catalog is public.
- [ ] The Movie contract and `NewRouter()` are unchanged, and the existing tests pass.
