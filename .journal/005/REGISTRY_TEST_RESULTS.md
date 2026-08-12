# Registry Compatibility Test Results

## Result labels

| Label | Meaning |
|---|---|
| PASS | The feature worked and its result was independently verified. |
| NO | The registry and library combination did not support the feature. |
| BLOCKED | A registry, account, permission, or environment setting prevented a valid test. |
| N/A | The registry behavior did not exercise this path. |

## Compatibility matrix

| Feature | Amazon ECR Private |
|---|---|
| HTTPS and authentication | PASS |
| Small blob, about 1 KiB | PASS |
| `Exists`, present and missing | PASS |
| Serial `Pull` | PASS |
| Progress reporting | PASS |
| `PullRange` | PASS |
| Parallel `Pull` | PASS |
| Parallel range-ignored fallback | N/A |
| Interrupted `Pull` resume | PASS |
| Unreferenced blob retrieval | PASS, observed |
| Monolithic `Push` | PASS |
| Empty blob `Push` and `Pull` | NO |
| Chunked `Push` | NO |
| Wrong-digest rejection | PASS |
| Exact-size rejection | PASS |
| Cross-repository `Mount` | PASS |
| Shared-client concurrency | PASS |
| Off-origin redirect credential scope | PASS |
| Upload `Location` handling | PASS |
| Retry after registry throttling | N/A |

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
