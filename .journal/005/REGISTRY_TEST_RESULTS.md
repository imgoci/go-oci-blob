# Registry Compatibility Test Results

## Result labels

| Label | Meaning |
|---|---|
| PASS | The feature worked and its result was independently verified. |
| NO | The registry and library combination did not support the feature. |
| BLOCKED | A registry, account, permission, or environment setting prevented a valid test. |
| N/A | The registry behavior did not exercise this path. |

## Compatibility matrix

| Feature | Amazon ECR Private | GHCR |
|---|---|---|
| HTTPS and authentication | PASS | PASS |
| Small blob, about 1 KiB | PASS | PASS |
| `Exists`, present and missing | PASS | PASS |
| Serial `Pull` | PASS | PASS |
| Progress reporting | PASS | PASS |
| `PullRange` | PASS | PASS |
| Parallel `Pull` | PASS | PASS |
| Parallel range-ignored fallback | N/A | N/A |
| Interrupted `Pull` resume | PASS | PASS |
| Unreferenced blob retrieval | PASS, observed | PASS, observed |
| Monolithic `Push` | PASS | PASS |
| Empty blob `Push` and `Pull` | NO | NO |
| Chunked `Push` | NO | PASS |
| Wrong-digest rejection | PASS | PASS |
| Exact-size rejection | PASS | PASS |
| Cross-repository `Mount` | PASS | PASS |
| Shared-client concurrency | PASS | PASS |
| Off-origin redirect credential scope | PASS | PASS |
| Upload `Location` handling | PASS | PASS |
| Retry after registry throttling | N/A | N/A |

## Amazon ECR Private

### Run identity

| Field | Value |
|---|---|
| Date | 2026-08-12 |
| Account | `803789966077` |
| Region | `us-east-2` |
| Registry host | `803789966077.dkr.ecr.us-east-2.amazonaws.com` |
| Library commit | `8700a0989bb82ca272ca986e2dc8eae79536d1b5` |
| Go | `go1.26.4 darwin/arm64` |
| ORAS | `1.3.0+unreleased`, built with Go 1.25.4 |
| Parallel configuration | 4 workers, 1 MiB chunks |
| Chunked-upload configurations | 1 MiB primary, 1 MiB repeat, 5 MiB diagnostic |
| Retry configuration | 3 attempts, 100 ms initial delay, 2 s maximum delay |
| Operation timeout | 90 seconds |
| `BLOB_MOUNTING` | `ENABLED` |

### Results

| Feature | Result | Observed result |
|---|---|---|
| HTTPS and authentication | PASS | Unauthenticated `/v2/` returned `401`; an ECR Basic-auth token returned `200`. |
| Independent seed control | PASS | ORAS seed, ECR layer availability, raw GET, and ORAS fetch agreed on exact bytes. |
| Small blob, 1,027 bytes | PASS | Independent seed and exact library and raw retrieval succeeded. |
| `Exists` | PASS | A present digest returned true through `HEAD 200`; a missing digest returned false through `HEAD 404`. |
| Serial `Pull` | PASS | Exact bytes, Go digest, independent SHA-256, progress, and verified EOF passed. |
| Progress reporting | PASS | Counts were monotonic, ended at the expected total, and did not overlap within the transfer. |
| `PullRange` | PASS | Beginning, middle, and tail windows returned exact bytes through `206`. Past-end returned `416`; a range crossing EOF was rejected. |
| Parallel `Pull` | PASS | Nine ranged requests completed through `206`. Four response bodies were active at once and zero remained active at completion. |
| Parallel range-ignored fallback | N/A | ECR served native ranges, so fallback was not used. |
| Interrupted `Pull` resume | PASS | A forced body break after 1 MiB resumed with a ranged `206` and returned exact bytes. |
| Unreferenced blob retrieval | PASS, observed | Before manifest linking, library Pull, raw GET, and ORAS fetch all returned exact bytes. |
| Monolithic `Push` | PASS | ECR returned `POST 202` then body-bearing `PUT 201`. The layer was available before linking and exact after independent verification. |
| Empty blob | NO | Reproduced twice, including in a fresh repository. ECR returned `PUT 400 BLOB_UPLOAD_INVALID` because the upload had no parts. |
| Chunked `Push` | NO | Reproduced with fresh digests at the 1 MiB setting and with a 5 MiB diagnostic. ECR advertised a 10 MiB minimum, then returned `PATCH 201 Created`; no tested chunked blob became available. |
| Wrong digest | PASS | ECR rejected the commit with `400`; neither the claimed nor calculated digest became available. The library attempted session cleanup. |
| Exact reader size | PASS | Short and trailing readers were rejected. Neither digest became available, and the library attempted session cleanup. |
| Cross-repository `Mount` | PASS | With mounting enabled and matching AES-256 repositories, ECR returned `201`; the library returned `(true, nil)`. ECR API, raw GET, SHA-256, and ORAS verified the destination. It contained zero manifests. |
| Missing-digest mount control | PASS | ECR returned `202`; the library returned `(false, nil)`, attempted session deletion, and left the destination absent. |
| Shared-client concurrency | PASS | Twenty-three barrier-started mixed operations passed under the race detector in 21.83 seconds. Four pushed artifacts were independently verified. No retryable status occurred. |
| Redirect credential scope | PASS | Blob GET used `307` to off-origin storage. No registry authorization, Cookie, Cookie2, proxy authorization, or Referer header crossed origins. |
| Upload `Location` | PASS | ECR returned same-origin absolute locations. The client followed their opaque state. |
| Throttling retry | N/A | The campaign observed no `429` or `5xx` response. |

### Mount rerun

The mount-only rerun used a unique 2,097,473-byte layer and fresh source and destination repositories with matching AES-256 encryption.

| Check | Result |
|---|---|
| Destination absent before mount | PASS |
| Mount request returned `201 Created` | PASS |
| `Docker-Content-Digest` matched | PASS |
| Library returned `(true, nil)` | PASS |
| No destination `PATCH` or `PUT` | PASS |
| Destination ECR layer availability | PASS |
| Destination raw GET and SHA-256 | PASS |
| Destination ORAS fetch | PASS |
| Destination manifest count remained zero | PASS |
| Source remained readable | PASS |
| Missing-digest `202` decline and cleanup | PASS |
| Race detector | PASS |

### Cleanup

All ECR test repositories were deleted and confirmed absent. The final count of repositories with the campaign prefix was zero. `BLOB_MOUNTING` remains `ENABLED` as requested.

### Evidence identities

| Evidence | SHA-256 |
|---|---|
| Full ECR campaign | `3a696b290802c016c47d91d89bb00ba4254e344d6dc070d9d339b3c39f67eb9a` |
| Empty-blob repeat | `4f5f1d6e479494baa17bcfdcc4540ced37f6cdfcb536e4e4aa481999c736adac` |
| Enabled mount rerun | `28c589f4711c8d18b4e318476bfaebfd2cf17f93b15284a6fa38673c441c68f7` |

## GitHub Container Registry

### Run identity

| Field | Value |
|---|---|
| Date | 2026-08-12 |
| Account | `jmgilman` |
| Registry host | `ghcr.io` |
| Library commit | `8700a0989bb82ca272ca986e2dc8eae79536d1b5` |
| Go | `go1.26.4 darwin/arm64` |
| ORAS | `1.3.0+unreleased`, built with Go 1.25.4 |
| Parallel configuration | 4 workers, 1 MiB chunks |
| Chunked-upload configuration | 1 MiB; the failure-only repeat and 5 MiB diagnostic were not needed |
| Retry configuration | 3 attempts, 100 ms initial delay, 2 s maximum delay |
| Operation timeout | 90 seconds |
| Authoritative package prefix | `go-oci-blob-coord-ghcr-20260812t184347z-78500-` |

### Results

| Feature | Result | Observed result |
|---|---|---|
| HTTPS and authentication | PASS | Unauthenticated `/v2/` returned `401`; the caller-supplied classic-token flow returned `200`. |
| Independent seed control | PASS | ORAS seed, raw HEAD and GET, and ORAS fetch agreed on the exact 8,388,865 bytes. |
| Small blob, 1,027 bytes | PASS | Independent seed and exact library and raw retrieval succeeded. |
| `Exists` | PASS | A present digest returned true through `HEAD 200`; a missing digest returned false through `HEAD 404`. |
| Serial `Pull` | PASS | Exact bytes and digest, monotonic progress, final total, and verified EOF passed. |
| Progress reporting | PASS | Counts were monotonic, ended at the expected total, and did not overlap within the transfer. |
| `PullRange` | PASS | Beginning, middle, and tail windows returned exact bytes through off-origin `206`; past-end returned `416`, and a range crossing EOF was rejected. |
| Parallel `Pull` | PASS | Nine ranged requests completed through off-origin `206`; two bodies overlapped and zero remained active at completion. |
| Parallel range-ignored fallback | N/A | GHCR storage served native ranges, so fallback was not used. |
| Interrupted `Pull` resume | PASS | A forced body break after 1 MiB resumed through a ranged off-origin `206` and returned exact bytes. |
| Unreferenced blob retrieval | PASS, observed | Before any manifest reference, library Pull, raw GET, and ORAS fetch all returned the exact completed monolithic blob. This was repeated in two fresh-package campaigns. |
| Monolithic `Push` | PASS | GHCR returned `POST 202` then a body-bearing `PUT 201`; independent raw and ORAS verification passed after manifest linking. |
| Empty blob | NO | Reproduced in two fresh-package campaigns. `HEAD` misleadingly reported the canonical empty digest as present, but an actual zero-byte upload returned `PUT 404 BLOB_UNKNOWN`. |
| Chunked `Push` | PASS | The 8,389,341-byte upload used nine 1 MiB-configured `PATCH 202` requests and a final `PUT 201`; HEAD, raw bytes, and independent verification passed. |
| Wrong digest | PASS | GHCR rejected the commit with `404 BLOB_UPLOAD_INVALID`; neither the claimed nor calculated digest became available. The library attempted session deletion, which GHCR answered with `405`. |
| Exact reader size | PASS | Short and trailing readers were rejected, neither possible digest became available, and the library attempted session deletion. GHCR answered both cleanup requests with `405`. |
| Cross-repository `Mount` | PASS | GHCR returned `POST 201`; the library returned `(true, nil)`, and the destination passed exact raw verification after an independent manifest link. |
| Shared-client concurrency | PASS | Twenty-one barrier-started mixed operations passed under the race detector in 10.91 seconds. Four pushed artifacts were independently verified; no ranged body remained active and no retryable status occurred. |
| Redirect credential scope | PASS | Blob reads used `307` to off-origin storage. No registry authorization, Cookie, Cookie2, proxy authorization, or Referer header crossed origins in library traffic. |
| Upload `Location` | PASS | GHCR returned relative upload locations. Successful and safely rejected operations followed their opaque state without retaining values in evidence. |
| Throttling retry | N/A | The campaign observed no `429` or `5xx` response. |

### Cleanup

The runner deleted both authoritative GHCR packages and polled the package API until each returned `404`. A separate paginated package-list audit found zero packages with the full campaign prefix or the broader `go-oci-blob-coord-ghcr-` prefix. No GitHub repository was created: GHCR package namespaces were sufficient, avoiding a repository that the available token could create but not delete.

### Evidence identities

| Evidence | SHA-256 |
|---|---|
| Authoritative GHCR campaign | `747b593adee3e0df09f3087794298f6a5aef09a3ce5997c1764605035879c8ee` |
| Disposable GHCR helper | `eeccf33d8bc05495da6a2706ec6e4a7ac53eb6c128ed49ba2eabc2ca192f28df` |
| Disposable GHCR matrix | `26b19cda2c1d7590df129be99c98efc5c25be00c3781e39f10eda7cd1ef6b736` |
| Disposable cleanup runner | `a44f948ec1d7030d23e64f6d0c31c42a01b4f25e38fa86b6918bb2f76181164e` |
