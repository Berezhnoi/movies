# Floci S3 notes (T1 spike, movie-trailers)

Date: 2026-11-08. Floci `floci/floci:2.2.0` was started with `make -C infra floci-up`. The client was a throwaway Go module (outside the repo) using AWS SDK v2 with `s3.New`, `BaseEndpoint`, `UsePathStyle: true` and static dummy keys. It ran in `golang:1.22` (go1.22.12, `GOTOOLCHAIN=local`) on the `floci` Docker network with the endpoint `http://floci:4566`.

## Results

| # | Check | Result | Details |
|---|-------|--------|---------|
| 1 | Path-style CreateBucket | pass | A second CreateBucket of the same bucket also succeeds (Floci is idempotent; real S3 can return `BucketAlreadyOwnedByYou`) |
| 2 | PutObject / HeadObject / GetObject | pass | HEAD returns the size and `video/mp4`; GET returns identical bytes |
| 3 | Overwrite the same key | pass | The second put replaces the first (size 100 → 200) |
| 4 | DeleteObject (existing) | pass | A later HEAD returns 404 `NotFound` |
| 5 | Range `bytes=0-99` | pass | Raw HTTP returns 206, `Content-Range: bytes 0-99/1000`, `Content-Length: 100`, `Accept-Ranges: bytes` |
| 6 | Range `900-`, `-50`, `990-2000` | pass | Correct `Content-Range` values; the end is clamped to the size |
| 7 | Range out of bounds `5000-6000` | pass | 416 `InvalidRange`, `Content-Range: bytes */1000` |
| 8 | Multi-range `0-1,5-9` | 416 | Floci rejects it. The app must not forward multi-range (see D5) |
| 9 | Missing key | pass | GET → 404 `NoSuchKey` (`errors.As(*types.NoSuchKey)` works); HEAD → 404 `NotFound` (`*types.NotFound` works); DELETE → success (no error) |
| 10 | Missing bucket | pass* | GET, PUT and DELETE → 404 with code `NoSuchBucket`, **but `errors.As(*types.NoSuchBucket)` is false**, so match on `smithy.APIError.ErrorCode()`. HEAD → 404 `NotFound`, the same as a missing key |
| 11 | Floci unreachable | as expected | No API error code; `*retry.MaxAttemptsError` wrapping a transport error with HTTP status 0 |
| 12 | `docker restart` | **data lost** | The bucket and objects are gone (HEAD bucket 404, GET `NoSuchBucket`). The `floci_data` volume holds no S3 data |
| 13 | `floci-down` + `floci-up` | **data lost** | Same as 12 |
| 14 | Auth | note | Floci accepted an **unsigned** raw GET. Anything that can reach port 4566 can read and write objects |
| 15 | K3s pod → `http://floci:4566` (Q-2) | not run | No cluster is running locally. The CoreDNS template answers every name in the `floci` zone, including `floci` itself, with the node IP, so this is expected to work; verify in T6 |

## N-1: SDK versions on Go 1.22

The newest versions whose `go.mod` declares `go 1.22` (the next releases declare 1.23 or 1.24) form one consistent release set:

- `github.com/aws/aws-sdk-go-v2 v1.39.2`
- `github.com/aws/aws-sdk-go-v2/service/s3 v1.88.4`
- `github.com/aws/aws-sdk-go-v2/credentials v1.18.16`
- `github.com/aws/smithy-go v1.23.0`, plus the internal modules that `go mod tidy` pulls in

| Acceptance check | Result |
|---|---|
| `go mod tidy` keeps `go 1.22` and adds no `toolchain` line | pass |
| `go build ./... && go vet ./...` on go1.22.12 with `GOTOOLCHAIN=local` | pass |
| All spike S3 calls work with these pins | pass |

Notes:
- The repo's `go.mod` moves from `go 1.21` to `go 1.22`, because these modules require it. That matches CI.
- The changelog of later SDK releases was not reviewed, so S3 fixes these pins miss are unknown.
- `credentials` pulls in `sso`, `ssooidc`, `sts` and `imds` as dependencies. They are not used at runtime with a static provider. `aws.CredentialsProviderFunc` would avoid the module entirely.

## Impact on the implementation (plan)

- **Error mapping (T5):**
  - object not found = `NoSuchKey`, `NotFound` or `NoSuchBucket`, matched by API error code
  - unavailable = no API error, i.e. a transport error, retries exhausted, or a 5xx
- **Bucket handling (D10, FR-11):** needed, because a restart always drops the bucket. Treat `BucketAlreadyOwnedByYou` and `BucketAlreadyExists` from CreateBucket as success for real-S3 parity.
- **Range (D5):** the handler validates ranges itself, forwards only one range, and for a multi-range request sends a plain GET and returns 200.
- **Delete (FR-8):** DeleteObject on a missing key succeeds. A missing bucket returns `NoSuchBucket`, which counts as already deleted (204).

## Impact on the PRD

No FR needs to change. FR-6, FR-7, FR-8 and FR-11 match the observed behaviour.

Proposed risk R-6 (not yet in the PRD): Floci does not check request signatures, and `infra/floci/docker-compose.yml` publishes `4566` on all interfaces, so anyone on the network can read or overwrite trailers. Accept it for the lab, or bind the port to `127.0.0.1`.
