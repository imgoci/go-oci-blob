# Registry Compatibility Test Results

## Result labels

| Label | Meaning |
|---|---|
| PASS | The feature worked and its result was independently verified. |
| NO | The registry and library combination did not support the feature. |
| BLOCKED | A registry, account, permission, or environment setting prevented a valid test. |
| N/A | The registry behavior did not exercise this path. |

## Compatibility matrix

| Feature | Amazon ECR Private | GHCR | Docker Hub | GCR URL, Artifact Registry-backed | Quay.io | Azure Container Registry | Harbor |
|---|---|---|---|---|---|---|---|
| HTTPS and authentication | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| Small blob, about 1 KiB | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| `Exists`, present and missing | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| Serial `Pull` | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| Progress reporting | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| `PullRange` | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| Parallel `Pull` | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| Parallel range-ignored fallback | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| Interrupted `Pull` resume | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| Unreferenced blob retrieval | PASS, observed | PASS, observed | PASS, observed | PASS, observed | PASS, observed | PASS, observed | PASS, observed |
| Monolithic `Push` | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| Empty blob `Push` and `Pull` | NO | NO | PASS | NO | NO | PASS | PASS |
| Chunked `Push` | NO | PASS | PASS | NO | PASS | PASS | PASS |
| Wrong-digest rejection | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| Exact-size rejection | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| Cross-repository `Mount` | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| Shared-client concurrency | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| Off-origin redirect credential scope | PASS | PASS | PASS | N/A | PASS | PASS | N/A |
| Upload `Location` handling | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| Retry after registry throttling | N/A | N/A | N/A | N/A | N/A | N/A | N/A |

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

## Docker Hub

### Run identity

| Field | Value |
|---|---|
| Date | 2026-08-12 |
| Account | `gilmanagents` |
| Registry host | `registry-1.docker.io` |
| Library commit | `8700a0989bb82ca272ca986e2dc8eae79536d1b5` |
| Go | `go1.26.4 darwin/arm64` |
| ORAS | `1.3.0+unreleased`, built with Go 1.25.4 |
| Parallel configuration | 4 workers, 1 MiB chunks |
| Chunked-upload configuration | 1 MiB; the failure-only repeat and 5 MiB diagnostic were not needed |
| Retry configuration | 3 attempts, 100 ms initial delay, 2 s maximum delay |
| Operation timeout | 90 seconds |
| Authoritative repository prefix | `go-oci-blob-coord-dh-20260812t194148z-40085-` |

### Results

| Feature | Result | Observed result |
|---|---|---|
| HTTPS and authentication | PASS | Unauthenticated `/v2/` returned `401`; a bearer token exchanged from the Bitwarden-sourced Docker Hub PAT returned `200`. |
| Independent seed control | PASS | ORAS seed, raw HEAD and GET, and ORAS fetch agreed on the exact 8,388,865 bytes. |
| Small blob, 1,027 bytes | PASS | Independent seed and exact library and raw retrieval succeeded. |
| `Exists` | PASS | A present digest returned true through `HEAD 200`; a missing digest returned false through `HEAD 404`. |
| Serial `Pull` | PASS | Exact bytes and digest, monotonic progress, final total, and verified EOF passed. |
| Progress reporting | PASS | Counts were monotonic, ended at the expected total, and did not overlap within the transfer. |
| `PullRange` | PASS | Beginning, middle, and tail windows returned exact bytes through off-origin `206`; past-end returned `416`, and a range crossing EOF was rejected. |
| Parallel `Pull` | PASS | Nine ranged requests completed through off-origin `206`; three bodies overlapped and zero remained active at completion. A calibration pass observed all four configured workers active. |
| Parallel range-ignored fallback | N/A | Docker Hub storage served native ranges, so fallback was not used. |
| Interrupted `Pull` resume | PASS | A forced body break after 1 MiB resumed through a ranged off-origin `206` and returned exact bytes. |
| Unreferenced blob retrieval | PASS, observed | Before any manifest reference, library Pull, raw GET, and ORAS fetch all returned the exact completed monolithic blob. |
| Monolithic `Push` | PASS | Docker Hub returned `POST 202` then a body-bearing `PUT 201`; independent raw and ORAS verification passed after manifest linking. |
| Empty blob | PASS | A fresh destination returned `HEAD 404`, then accepted `POST 202` and zero-byte `PUT 201`; raw and library reads returned exactly zero bytes after independent manifest linking. |
| Chunked `Push` | PASS | The 8,389,341-byte upload used nine 1 MiB-configured `PATCH 202` requests and a final `PUT 201`; HEAD and exact raw verification passed. |
| Wrong digest | PASS | Docker Hub rejected the commit with `400 DIGEST_INVALID`; neither the claimed nor calculated digest became available. The library attempted session deletion, whose authenticated request received `500`. |
| Exact reader size | PASS | Short and trailing readers were rejected, neither possible digest became available, and both authenticated cleanup requests returned `204`. |
| Cross-repository `Mount` | PASS | Docker Hub returned `POST 201`; the library returned `(true, nil)`, and the destination passed exact raw verification after an independent manifest link. |
| Shared-client concurrency | PASS | Twenty-one barrier-started mixed operations passed under the race detector in 11.73 seconds. Four pushed artifacts were independently verified; no ranged body remained active and no retryable status occurred. |
| Redirect credential scope | PASS | Blob reads used `307` to off-origin storage. No registry authorization, Cookie, Cookie2, proxy authorization, or Referer header crossed origins in library traffic. |
| Upload `Location` | PASS | Docker Hub returned same-origin absolute upload locations. Successful and safely rejected operations followed their opaque state without retaining values in evidence. |
| Throttling retry | N/A | The campaign observed no throttled transfer. One isolated cleanup `DELETE` returned `500` without a successful retry, which is not positive retry evidence. |

### Harness calibration

Docker Hub's bearer challenge contains the quoted scope value `pull,push`. The first disposable parser incorrectly split that quoted comma, obtained a pull-only token, and produced write-side `401` responses even though ORAS writes succeeded. A raw same-PAT control proved `POST 202`; replacing the disposable parser with quote-aware handling resolved every write path. These were harness calibration runs and are not registry compatibility results.

### Cleanup

Docker Hub accepted repository deletion asynchronously with `202`. The runner polled both authoritative repositories until the API returned `404`, then independently required their exact prefix count to be zero. A broader audit found zero repositories under every `go-oci-blob-` test and calibration prefix. The PAT occurred zero times in the evidence and harness files. The disposable source export remained read-only and the main checkout remained unchanged.

### Evidence identities

| Evidence | SHA-256 |
|---|---|
| Authoritative Docker Hub campaign | `4436cd505e1d0b578499639823a60d02756d885e75e4aba6521247ce462c2c0f` |
| Disposable Docker Hub helper | `7c96da36f94047a23e0db3445c057e92e6f808bb71b9a831cf73696f13e6afe6` |
| Disposable Docker Hub matrix | `46d882b54f1ff6eca90e241e23b8a65159b245dc2dddad5d4b5302967dd3257c` |
| Disposable cleanup runner | `47089799d4e6877eaaf93ce62a210f942c393e435a7ce142bff06b44e63e9bcd` |

## Google Container Registry URL

This campaign targeted `gcr.io`, but Google has retired the legacy Container Registry backend. The shared project's `REDIRECTION_FROM_GCR_IO_ENABLED` setting routed the hostname to an Artifact Registry `gcr.io` repository in the `us` multi-region. The compatibility results therefore describe the current `gcr.io` URL surface, not the retired service.

### Run identity

| Field | Value |
|---|---|
| Date | 2026-08-12 |
| Project | `agents-shared-505304` |
| Registry host | `gcr.io` |
| Serving backend | Artifact Registry `projects/agents-shared-505304/locations/us/repositories/gcr.io` |
| Redirection state | `REDIRECTION_FROM_GCR_IO_ENABLED` |
| Library commit | `8700a0989bb82ca272ca986e2dc8eae79536d1b5` |
| Go | `go1.26.4 darwin/arm64` |
| ORAS | `1.3.0+unreleased`, built with Go 1.25.4 |
| Parallel configuration | 4 workers, 256 KiB chunks |
| Chunked-upload configuration | 1 MiB |
| Retry configuration | 4 attempts, 100 ms initial delay, 2 s maximum delay |
| Campaign timeout | 12 minutes |
| Authoritative namespace | `agents-shared-505304/go-oci-blob-ft-20260812200432` |

### Results

| Feature | Result | Observed result |
|---|---|---|
| HTTPS and authentication | PASS | Unauthenticated `/v2/` returned `401`; a caller-supplied bearer flow backed by the Bitwarden service-account credential returned `200` over HTTPS. |
| Small blob, 1,027 bytes | PASS | Library Push succeeded and an independent authenticated GET returned exact bytes and digest. |
| `Exists` | PASS | A present digest returned true; a missing digest returned false without an error. |
| Serial `Pull` | PASS | The full 3,146,061-byte body reached verified EOF with exact bytes and an independent SHA-256 match. |
| Progress reporting | PASS | Counts were monotonic, ended at the exact byte total, and did not overlap within the transfer. |
| `PullRange` | PASS | Beginning, middle, and tail windows were exact. Past-end and crossing-EOF ranges were rejected. |
| Parallel `Pull` | PASS | Seventeen ranged `206` responses returned exact ordered bytes; three response bodies overlapped and zero remained active. The race run reproduced the same result. |
| Parallel range-ignored fallback | N/A | `gcr.io` served native ranges, so fallback was not used. |
| Interrupted `Pull` resume | PASS | A consumer-injected body break resumed with three ranged requests and returned exact bytes. |
| Unreferenced blob retrieval | PASS, observed | Immediately after monolithic completion and before any manifest existed, raw GET and independent ORAS blob fetch returned the exact 3,146,061 bytes and digest. |
| Monolithic `Push` | PASS | The default path opened an upload and completed with `PUT 201`; independent raw GET verified the exact payload. |
| Empty blob | NO | The registry opened the upload, then rejected the zero-byte commit with `PUT 400 Bad Request`; the canonical empty digest remained unavailable. |
| Chunked `Push` | NO | The first 1 MiB `PATCH` returned `202`, the next session request returned `405 Method Not Allowed`, and the digest remained unavailable. |
| Wrong digest | PASS | The commit failed, both claimed and calculated digests remained absent, and the library's best-effort session DELETE returned `204`. |
| Exact reader size | PASS | Both short and trailing declarations failed, the digest remained absent, and both best-effort session DELETE requests returned `204`. |
| Cross-repository `Mount` | PASS | The registry returned `201`; the destination passed an independent exact-byte GET. |
| Shared-client concurrency | PASS | Twelve barrier-started mixed Pull, PullRange, Exists, Push, and Mount operations completed on shared clients. Every pushed and mounted blob passed an independent exact-byte GET. |
| Redirect credential scope | N/A | No off-origin redirect occurred in the campaign; every response stayed on `gcr.io`. |
| Upload `Location` | PASS | Successful uploads followed relative opaque locations through final `PUT 201`. |
| Throttling retry | N/A | No safe deterministic hosted throttling trigger was available and no `429` or `5xx` response occurred. |

### Verification and cleanup

The normal campaign and its `GOMAXPROCS=8` race run both produced 15 PASS, two NO, three N/A, and zero FAIL rows. The race detector reported no race. ORAS independently fetched the unreferenced 3,146,061-byte blob and matched `sha256:3c219121fac4cc317d0b4046539d879a724b17b492b430589f30033151419b79`.

Preflight found redirection enabled, no `gcr.io` repository, and zero Artifact Registry repositories in the project. The first push auto-created the predefined `us/gcr.io` repository. After testing, the exact repository creation time was checked, the repository was deleted through its long-running operation, and readback returned `404`. The final project repository count returned to zero, while the pre-existing redirection setting remained enabled. The repository contained no packages or Docker images because this campaign intentionally exercised blob APIs without publishing manifests.

### Evidence identities

| Evidence | SHA-256 |
|---|---|
| Authoritative GCR matrix | `94a226cd9c2a43bc9b3671b85831a59d6321a3e1e9615832f1700ec66e7105db` |
| Race GCR matrix | `94a226cd9c2a43bc9b3671b85831a59d6321a3e1e9615832f1700ec66e7105db` |
| Disposable GCR wire evidence | `4d2a031596591ee4dcbccf291ec4d095b36bcf0059bdeb31b0ad52f338aa954b` |
| Race GCR wire evidence | `1220e880e9cbfb187fd8d2ebcc34d707564f407f560c22f2046a4e0d8b501726` |
| Disposable GCR matrix harness | `f71e3a32c662d0240fe5235a2de64a5fc0e10b55bdba27785c64706e686fd30d` |

## Quay.io

### Run identity

| Field | Value |
|---|---|
| Date | 2026-08-12 |
| Account | `gilmanagents` |
| Registry host | `quay.io` |
| Credential model | Disposable robot with write access limited to the two campaign repositories |
| Library commit | `8700a0989bb82ca272ca986e2dc8eae79536d1b5` |
| Go | `go1.26.4 darwin/arm64` |
| ORAS | `1.3.0+unreleased`, built with Go 1.25.4 |
| Parallel configuration | 4 workers, 256 KiB chunks |
| Chunked-upload configuration | 1 MiB |
| Retry configuration | 4 attempts, 100 ms initial delay, 2 s maximum delay |
| Campaign timeout | 12 minutes |
| Source repository | `gilmanagents/go-oci-blob-ft-20260812202302-source` |
| Destination repository | `gilmanagents/go-oci-blob-ft-20260812202302-dest` |

### Results

| Feature | Result | Observed result |
|---|---|---|
| HTTPS and authentication | PASS | A caller-supplied bearer flow backed by the disposable robot returned `200` from `/v2/` over HTTPS. |
| Small blob, 1,027 bytes | PASS | Library Push succeeded and an independent GET returned all 1,027 exact bytes. |
| `Exists` | PASS | A present digest returned true and a fresh missing digest returned false without errors. |
| Serial `Pull` | PASS | The full 2,097,289-byte body reached verified EOF with exact bytes and digest. Independent ORAS retrieval matched the same size and SHA-256. |
| Progress reporting | PASS | Counts were monotonic, ended at the exact byte total, and did not overlap within the transfer. |
| `PullRange` | PASS | Beginning, middle, and tail windows were exact through native `206` responses. A past-end request was rejected. |
| Parallel `Pull` | PASS | Nine ranged `206` responses returned exact ordered bytes. Two response bodies overlapped in both final normal and race runs, and zero remained active. |
| Parallel range-ignored fallback | N/A | Quay served native ranges, so fallback was not used. |
| Interrupted `Pull` resume | PASS | A consumer-injected body break after 128 KiB resumed through ranged requests and returned exact bytes. |
| Unreferenced blob retrieval | PASS, observed | Before any manifest existed, raw GET and independent ORAS blob fetch returned the exact completed 2,097,289-byte blob and digest. |
| Monolithic `Push` | PASS | The default path used a body-bearing final PUT with no PATCH, and independent raw retrieval returned exact bytes. |
| Empty blob | NO | Quay accepted the zero-byte Push, and `Exists` then reported true, but raw GET and library Pull returned `404`. An independent ORAS blob fetch also returned not found. |
| Chunked `Push` | PASS | A 3,146,061-byte upload used four 1 MiB-configured PATCH requests and completed successfully; independent raw retrieval returned exact bytes. |
| Wrong digest | PASS | The upload failed, neither the claimed nor calculated digest became available, and the library attempted session cleanup. |
| Exact reader size | PASS | A trailing-byte declaration failed, the digest remained absent, and the library attempted session cleanup. |
| Cross-repository `Mount` | PASS | Quay returned a successful mount; the destination passed an independent exact-byte GET. |
| Shared-client concurrency | PASS | Twenty concurrent Pull, PullRange, Exists, Push, and Mount operations completed without errors. The mounted result passed an independent exact-byte GET, and the race detector reported no race. |
| Redirect credential scope | PASS | The campaign observed 43 off-origin storage requests. No registry authorization header crossed origins. |
| Upload `Location` | PASS | Successful uploads followed same-origin absolute opaque locations; 58 such responses were observed across the final campaign. |
| Throttling retry | N/A | No safe deterministic hosted throttling trigger was available and no controlled `429` response was produced. |

### Verification and cleanup

The final normal and race campaigns each produced 17 PASS, one NO, and two N/A rows. The normal run completed in 21.43 seconds; the race test completed in 20.79 seconds with no race report. An independent ORAS control fetched and hash-verified the 2,097,289-byte main blob, while its empty-digest fetch independently reproduced the registry's not-found response.

Both uniquely named public repositories were deleted and the account repository list returned to zero. The disposable robot was deleted and the account reported no robot accounts. The immutable source export had zero user-writable files, and the main checkout remained clean at the tested commit.

### Evidence identities

| Evidence | SHA-256 |
|---|---|
| Final normal Quay matrix | `85e221dccc9cf7bef6ba62e3e303bee2f606524d5c6c64fa1513f0f5f5e0a46e` |
| Final race Quay matrix | `1d48ebe090f359e6186456c59f39c432c1d4d919ce89315c8f97730af160efa3` |
| Disposable Quay matrix harness | `3c6d8fdc36a7c0dc18491ff437177ec50f05a70ce28a0dab0fa463dccb3bab8a` |

## Azure Container Registry

### Run identity

| Field | Value |
|---|---|
| Date | 2026-08-12 |
| Subscription | `Pay-As-You-Go` (`cf14054f-1892-4519-a74c-8e1a086b4764`) |
| Tenant | `1de2c188-1b00-4f6e-8343-cb43f983d4b4` |
| Region | `westus2` |
| Registry | `goociblobft220000.azurecr.io` |
| SKU | Basic |
| Credential model | Temporary registry admin credential scoped to the disposable ACR |
| Library commit | `8700a0989bb82ca272ca986e2dc8eae79536d1b5` |
| Go | `go1.26.4 darwin/arm64` |
| ORAS | `1.3.0+unreleased`, built with Go 1.25.4 |
| Parallel configuration | 4 workers, 256 KiB chunks |
| Chunked-upload configuration | 1 MiB |
| Retry configuration | 4 attempts, 100 ms initial delay, 2 s maximum delay |
| Campaign timeout | 12 minutes |
| Source namespace | `go-oci-blob-ft-20260812t220000z-source` |
| Destination namespace | `go-oci-blob-ft-20260812t220000z-dest` |

### Results

| Feature | Result | Observed result |
|---|---|---|
| HTTPS and authentication | PASS | Unauthenticated `/v2/` returned `401`; a caller-supplied bearer flow backed by the temporary ACR admin credential returned `200` over HTTPS. |
| Small blob, 1,027 bytes | PASS | Library Push succeeded and an independent authenticated GET returned exact bytes. |
| `Exists` | PASS | A present digest returned true and a fresh missing digest returned false without errors. |
| Serial `Pull` | PASS | The full 2,097,289-byte body reached verified EOF with exact bytes and digest. Independent ORAS retrieval matched its size and SHA-256. |
| Progress reporting | PASS | Counts were monotonic, ended at the exact total, and did not overlap within the transfer. |
| `PullRange` | PASS | Beginning, middle, and tail windows were exact through native `206` responses. A past-end request was rejected. |
| Parallel `Pull` | PASS | Nine ranged `206` responses returned exact ordered bytes. Three response bodies overlapped in the final runs and none remained active. |
| Parallel range-ignored fallback | N/A | ACR served native ranges, so fallback was not used. |
| Interrupted `Pull` resume | PASS | A consumer-injected body break after 128 KiB resumed through ranged requests and returned exact bytes. |
| Unreferenced blob retrieval | PASS, observed | Before any manifest existed, raw GET and independent ORAS blob fetch returned the exact completed 2,097,289-byte blob and digest. |
| Monolithic `Push` | PASS | The default path used a body-bearing final PUT with no PATCH, and independent retrieval returned exact bytes. |
| Empty blob | PASS | ACR accepted the zero-byte Push; `Exists`, raw GET, library Pull, and independent ORAS fetch all returned the exact empty blob. |
| Chunked `Push` | PASS | A 3,146,061-byte upload used four 1 MiB-configured PATCH requests and completed successfully; independent raw retrieval returned exact bytes. |
| Wrong digest | PASS | The upload failed, neither the claimed nor calculated digest became available, and the library attempted session cleanup. |
| Exact reader size | PASS | Both short and trailing reader declarations failed, and the digest remained absent. |
| Cross-repository `Mount` | PASS | ACR returned a successful mount and the destination passed an independent exact-byte GET. |
| Shared-client concurrency | PASS | Twenty concurrent Pull, PullRange, Exists, Push, and Mount operations completed without errors. The mounted result passed an independent exact-byte GET, and the race detector reported no race. |
| Redirect credential scope | PASS | The campaign observed 62 off-origin storage requests. No registry authorization header crossed origins. |
| Upload `Location` | PASS | Successful operations followed both absolute and relative opaque upload locations. The final campaign observed 62 absolute and 30 relative responses. |
| Throttling retry | N/A | No safe deterministic hosted throttling trigger was used and no controlled `429` response was produced. |

### Verification and cleanup

The final normal and `GOMAXPROCS=8` race campaigns each produced 18 PASS and two N/A rows. The normal test completed in 8.77 seconds; the race test completed in 8.08 seconds with no race report. ORAS independently fetched and hash-verified the 2,097,289-byte main blob and separately fetched the exact zero-byte blob.

Preflight found one unrelated resource group named `Lab`, zero ACR instances, and the `Microsoft.ContainerRegistry` provider in `NotRegistered` state. The campaign created a uniquely tagged resource group containing exactly one Basic ACR and left `Lab` untouched. Cleanup deleted the whole disposable resource group, confirmed the subscription again had zero ACR instances, and restored the provider to `Unregistered`. The immutable source export had zero user-writable files and the main checkout remained clean.

### Evidence identities

| Evidence | SHA-256 |
|---|---|
| Final normal ACR matrix | `0812e1e5f580de8c0df699bcf309bf3290109bab38ccb6920efa7c3bd3b83b78` |
| Final race ACR matrix | `0812e1e5f580de8c0df699bcf309bf3290109bab38ccb6920efa7c3bd3b83b78` |
| Disposable ACR matrix harness | `bc9ece8a3b0b5a491eca8f7700110077f8b403b8197b8c9ebef6d983117e3b6d` |

## Harbor

### Run identity

| Field | Value |
|---|---|
| Date | 2026-08-12 |
| Harbor | `v2.15.2`, official online installer |
| Registry host | `harbor.localtest.me:9443` |
| Deployment | Disposable local Docker Compose; filesystem storage; private source and destination projects |
| Platform | Harbor `linux/amd64` images under Docker emulation on a `linux/arm64` host |
| Credential model | Disposable local Harbor admin credential over a campaign-only CA |
| Library commit | `8700a0989bb82ca272ca986e2dc8eae79536d1b5` |
| Go | `go1.26.4 darwin/arm64` |
| ORAS | `1.3.0+unreleased`, built with Go 1.25.4 |
| Parallel configuration | 4 workers, 256 KiB chunks |
| Chunked-upload configuration | 1 MiB |
| Retry configuration | Library default, except a bounded 4-attempt resume probe |
| Operation timeout | 30 seconds per operation; 10 minutes per campaign |
| Source project | `blob-ft-wag3ib-source` |
| Destination project | `blob-ft-wag3ib-dest` |

### Results

| Feature | Result | Observed result |
|---|---|---|
| HTTPS and authentication | PASS | The campaign-only CA verified Harbor's TLS certificate. Anonymous `/v2/` returned a `401` Bearer challenge, and scoped tokens obtained with the disposable credential authenticated data-plane requests. |
| Small blob, 1,027 bytes | PASS | Library Push succeeded and an independent authenticated GET returned all exact bytes. |
| `Exists` | PASS | A present digest returned true and a synthetic missing digest returned false without an error. |
| Serial `Pull` | PASS | An ORAS-seeded 4 MiB layer reached verified EOF with exact bytes and an independent SHA-256 match. |
| Progress reporting | PASS | Parallel Pull callbacks were monotonic and non-overlapping and ended at `4,194,304 / 4,194,304`; each final run observed 38 callbacks. |
| `PullRange` | PASS | A 333,777-byte middle window returned exact bytes through Harbor's native range support. |
| Parallel `Pull` | PASS | Sixteen successful `206` requests returned exact ordered bytes. All four configured response bodies overlapped, and zero remained active after completion. |
| Parallel range-ignored fallback | N/A | Harbor served native ranges, so fallback was not used. |
| Interrupted `Pull` resume | PASS | A consumer-injected mid-body failure resumed with a ranged continuation and returned exact final bytes and digest. |
| Unreferenced blob retrieval | PASS, observed | A completed library Push was independently retrievable before any manifest referenced it. |
| Monolithic `Push` | PASS | The default path used exactly one body-bearing final PUT and no PATCH; an independent GET returned exact bytes. |
| Empty blob | PASS | Harbor accepted the canonical zero-byte blob; `Exists` and independent GET both verified it. |
| Chunked `Push` | PASS | A 3,145,839-byte upload used four acknowledged PATCH requests and an empty final `PUT 201`; independent retrieval returned exact bytes. |
| Wrong digest | PASS | Push failed, neither the claimed nor calculated digest became available, and an upload-session cleanup DELETE was observed. |
| Exact reader size | PASS | Both short and trailing reader declarations failed, and the claimed digest remained absent. |
| Cross-repository `Mount` | PASS | The destination was absent first; exactly one mount POST returned `201`; raw GET and an independent ORAS fetch returned exact destination bytes. |
| Shared-client concurrency | PASS | Twenty barrier-started Pull, PullRange, Exists, Push, and Mount operations completed. All eight pushed or mounted artifacts passed independent exact-byte GETs, and the race detector reported no race. |
| Redirect credential scope | N/A | Filesystem-backed Harbor emitted no off-origin redirect, so the credential-stripping path was not exercised. |
| Upload `Location` | PASS | Successful operations followed Harbor's same-origin absolute opaque upload locations. |
| Throttling retry | N/A | The bounded local campaign produced no `429` response. |

### Verification and cleanup

The normal and race campaigns produced identical matrices: 17 PASS, three N/A, and zero NO, BLOCKED, or FAIL rows. The normal test completed in 25.64 seconds; the race test completed in 26.17 seconds with no race report. ORAS independently seeded the source layer and later fetched the mounted destination from both fresh normal and race repository paths with exact bytes.

Harbor ran as nine healthy `v2.15.2` service containers. Cleanup stopped and removed the exact Compose deployment and network, removed both private projects with the disposable filesystem data, deleted all campaign TLS, credentials, source export, fixtures, harness, logs, and evidence, and confirmed port 9443 was closed. The unrelated pre-existing GitHub MCP container was not touched. The main checkout remained clean at the tested commit.

### Evidence identities

| Evidence | SHA-256 |
|---|---|
| Final normal Harbor matrix | `0e891541b792c5ff62d597d7382cc765f79cbcfcd0716c4885af7b5e279dcb8d` |
| Final race Harbor matrix | `0e891541b792c5ff62d597d7382cc765f79cbcfcd0716c4885af7b5e279dcb8d` |
| Disposable Harbor matrix harness | `4d3191d3509de833b7d33b81a72acd10c3b7cc6988c68bfb78ac93419cac0263` |
| Harbor core image | `sha256:06396e2c823b582ae3da9b8d471bd250e4934d10b864f6c627546707af32cefc` |
