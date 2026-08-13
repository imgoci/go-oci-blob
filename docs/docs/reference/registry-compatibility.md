# Registry compatibility

Results of running one library test campaign against nine registries.
Hosted-registry behavior can change at any time; treat each cell as a dated
observation, not a permanent property.

## Verification identity

| Field | Value |
|---|---|
| Campaign date | 2026-08-12 |
| Library commit | `8700a0989bb82ca272ca986e2dc8eae79536d1b5` |
| Go | `go1.26.4 darwin/arm64` |
| Independent control | ORAS CLI plus raw HTTP requests |
| Parallel configuration | 4 workers, 1 MiB chunks (256 KiB on `gcr.io`) |
| Chunked configuration | 1 MiB chunks |

The refresh procedure is
[Refresh the registry compatibility matrix](../how-to/refresh-registry-compatibility.md).

## Registries

| Column | Registry |
|---|---|
| ECR | Amazon ECR Private |
| GHCR | GitHub Container Registry |
| Hub | Docker Hub |
| gcr.io | The `gcr.io` URL served by Google Artifact Registry |
| Quay | Quay.io |
| ACR | Azure Container Registry |
| Harbor | Harbor v2.15.2, self-hosted, filesystem storage |
| GitLab | GitLab CE 19.2.1 native registry, self-hosted, filesystem storage |
| Nexus | Sonatype Nexus Repository OSS 3.76.0, self-hosted, filesystem storage |

## Result labels

| Label | Meaning |
|---|---|
| PASS | The feature worked and its result was independently verified. |
| NO | The registry and library combination did not support the feature. |
| N/A | The campaign did not exercise this path on this registry. |

## Matrix

| Feature | ECR | GHCR | Hub | gcr.io | Quay | ACR | Harbor | GitLab | Nexus |
|---|---|---|---|---|---|---|---|---|---|
| HTTPS and authentication | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| Small blob (about 1 KiB) | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| `Exists`, present and missing | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| Serial `Pull` | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| Progress reporting | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| `PullRange` | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| Parallel `Pull` | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| Parallel range-ignored fallback | N/A | N/A | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| Interrupted `Pull` resume | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| Unreferenced blob retrieval¹ | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| Monolithic `Push` | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| Empty blob `Push` and `Pull` | NO | NO | PASS | NO | NO | PASS | PASS | PASS | PASS |
| Chunked `Push` | NO | PASS | PASS | NO | PASS | PASS | PASS | PASS | PASS |
| Wrong-digest rejection | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS | NO |
| Exact-size rejection | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS | NO |
| Cross-repository `Mount` | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS | NO |
| Shared-client concurrency | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| Off-origin redirect credential scope | PASS | PASS | PASS | N/A | PASS | PASS | N/A | N/A | N/A |
| Upload `Location` handling | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| Retry after registry throttling | N/A | N/A | N/A | N/A | N/A | N/A | N/A | N/A | N/A |

¹ Observed behavior, not a documented registry guarantee: every registry
served a pushed blob before any manifest referenced it, but a registry may
garbage-collect unreferenced blobs at any time.

## Observed limits

### Empty blobs

The zero-byte blob (`sha256:e3b0c4...b855`) is rejected or lost on four
registries:

- **ECR**: the commit returned `400 BLOB_UPLOAD_INVALID` because the upload
  had no parts.
- **GHCR**: `HEAD` reported the canonical empty digest as present, but a
  zero-byte upload returned `404 BLOB_UNKNOWN`.
- **gcr.io**: the zero-byte commit returned `400 Bad Request`.
- **Quay.io**: the zero-byte `Push` succeeded and `Exists` reported true, but
  every retrieval — library `Pull`, raw `GET`, and ORAS — returned `404`.

### Chunked upload

- **ECR**: advertised a 10 MiB minimum chunk length, acknowledged every
  `PATCH`, and never made any tested chunked blob available.
- **gcr.io**: accepted the first `PATCH` with `202` and answered the next
  upload request with `405 Method Not Allowed`.

### Nexus Repository OSS 3.76.0

- **Wrong-digest rejection**: the commit returned `400` and `Push` returned
  an error, but the claimed digest afterwards answered `HEAD 200` and served
  bytes whose SHA-256 differs from it. A verified library `Pull` of that
  digest returns `ErrDigestMismatch`.
- **Exact-size rejection**: a reader with trailing data was rejected, but in
  three of five campaigns the declared prefix was committed anyway.
- **Cross-repository `Mount`**: the mount request between two hosted Docker
  repositories returned `202`; `Mount` returns `(false, nil)` and the caller
  falls back to `Push`.

### Amazon ECR mount setting

Cross-repository `Mount` on ECR requires the regional registry's
`BLOB_MOUNTING` setting to be `ENABLED`. With mounting disabled, ECR declines
the mount and `Mount` returns `(false, nil)`.

### Paths no registry exercised

- **Parallel range-ignored fallback**: every registry served native ranged
  requests, so the single-stream fallback never triggered. Deterministic
  consumer tests cover the library path.
- **Retry after registry throttling**: no campaign observed a `429` or `5xx`
  response.
