# Registry compatibility

Results of running one library test campaign against nine registries.
Hosted-registry behavior can change at any time; treat each cell as a dated
observation, not a permanent property. Hover over a feature name for what its
row verified.

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

## Result labels

| Symbol | Label | Meaning |
|---|---|---|
| :material-check:{ .result-pass title="PASS" } | PASS | The feature worked and its result was independently verified. |
| :material-close:{ .result-no title="NO" } | NO | The registry and library combination did not support the feature. |
| :material-minus:{ .result-na title="N/A" } | N/A | The campaign did not exercise this path on this registry. |

## Differences at a glance

Every registry passed authenticated reads, ranged and parallel pulls, resume,
monolithic push, concurrency, and upload `Location` handling. The registries
differ only here:

- **Empty blob `Push` and `Pull`** — rejected or lost on four registries: ECR
  and `gcr.io` reject the zero-byte commit with `400`, GHCR reports the empty
  digest present but answers uploads and reads with `404`, and Quay.io accepts
  the push while every retrieval returns `404`.
- **Chunked `Push`** — broken on ECR, which acknowledges every chunk and never
  makes the blob available, and on `gcr.io`, which answers the second upload
  request with `405`.
- **Wrong-digest rejection, exact-size rejection, and cross-repository
  `Mount`** — unsupported on Nexus Repository OSS 3.76.0: a wrong-digest
  commit still becomes retrievable (a verified `Pull` of it returns
  `ErrDigestMismatch`), a trailing-data upload can commit the declared prefix,
  and mounts are declined so `Mount` returns `(false, nil)`.

Cross-repository `Mount` on ECR requires the regional registry's
`BLOB_MOUNTING` setting to be `ENABLED`; with it disabled, ECR declines the
mount.

Two paths produced no result anywhere: no registry ignored ranged requests
(the parallel fallback never triggered) and none answered with `429` or `5xx`
(retry after throttling was not observed).

## Results by registry

=== "ECR"

    Amazon ECR Private.

    | Feature | Result |
    |---|:---:|
    | <span title="The registry rejects unauthenticated /v2/ requests and accepts the campaign credential over HTTPS.">HTTPS and authentication</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Push and pull of a roughly 1 KiB blob, verified byte-for-byte against independent controls.">Small blob (about 1 KiB)</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Exists returns true for a stored digest and false, without an error, for a missing one.">`Exists`, present and missing</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Single-stream download returning exact bytes and a digest-verified end of stream.">Serial `Pull`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Progress counts are monotonic, end at the exact byte total, and do not overlap within one transfer.">Progress reporting</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Ranged reads return exact byte windows; past-end and EOF-crossing ranges are rejected.">`PullRange`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Concurrent ranged workers reassemble the blob in order through the same digest-verifying reader.">Parallel `Pull`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="When a registry ignores ranged requests, parallel Pull falls back to a single stream.">Parallel range-ignored fallback</span> | :material-minus:{ .result-na title="N/A" } |
    | <span title="A connection broken mid-body resumes with a ranged request and still returns exact bytes.">Interrupted `Pull` resume</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="A pushed blob is retrievable before any manifest references it. Observed behavior, not a guarantee: a registry may garbage-collect unreferenced blobs at any time.">Unreferenced blob retrieval</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="The default single-PUT upload, independently verified after commit.">Monolithic `Push`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Uploading and retrieving the canonical zero-byte blob.">Empty blob `Push` and `Pull`</span> | :material-close:{ .result-no title="NO" } |
    | <span title="The opt-in PATCH-chunked upload, committed and independently verified.">Chunked `Push`</span> | :material-close:{ .result-no title="NO" } |
    | <span title="A commit under a digest that does not match the uploaded bytes is rejected, and neither digest becomes retrievable.">Wrong-digest rejection</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Readers yielding fewer or more bytes than the declared size are rejected without committing anything.">Exact-size rejection</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Mounting an existing blob from a source repository into a destination repository without re-uploading it.">Cross-repository `Mount`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Mixed concurrent operations on one shared client complete correctly under the race detector.">Shared-client concurrency</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="No registry Authorization, cookie, or Referer header follows a redirect to off-origin blob storage.">Off-origin redirect credential scope</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Upload sessions follow relative and absolute Location URLs, preserving their opaque query state.">Upload `Location` handling</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Automatic retry after a 429 or 5xx response from the registry.">Retry after registry throttling</span> | :material-minus:{ .result-na title="N/A" } |

=== "GHCR"

    GitHub Container Registry.

    | Feature | Result |
    |---|:---:|
    | <span title="The registry rejects unauthenticated /v2/ requests and accepts the campaign credential over HTTPS.">HTTPS and authentication</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Push and pull of a roughly 1 KiB blob, verified byte-for-byte against independent controls.">Small blob (about 1 KiB)</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Exists returns true for a stored digest and false, without an error, for a missing one.">`Exists`, present and missing</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Single-stream download returning exact bytes and a digest-verified end of stream.">Serial `Pull`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Progress counts are monotonic, end at the exact byte total, and do not overlap within one transfer.">Progress reporting</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Ranged reads return exact byte windows; past-end and EOF-crossing ranges are rejected.">`PullRange`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Concurrent ranged workers reassemble the blob in order through the same digest-verifying reader.">Parallel `Pull`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="When a registry ignores ranged requests, parallel Pull falls back to a single stream.">Parallel range-ignored fallback</span> | :material-minus:{ .result-na title="N/A" } |
    | <span title="A connection broken mid-body resumes with a ranged request and still returns exact bytes.">Interrupted `Pull` resume</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="A pushed blob is retrievable before any manifest references it. Observed behavior, not a guarantee: a registry may garbage-collect unreferenced blobs at any time.">Unreferenced blob retrieval</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="The default single-PUT upload, independently verified after commit.">Monolithic `Push`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Uploading and retrieving the canonical zero-byte blob.">Empty blob `Push` and `Pull`</span> | :material-close:{ .result-no title="NO" } |
    | <span title="The opt-in PATCH-chunked upload, committed and independently verified.">Chunked `Push`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="A commit under a digest that does not match the uploaded bytes is rejected, and neither digest becomes retrievable.">Wrong-digest rejection</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Readers yielding fewer or more bytes than the declared size are rejected without committing anything.">Exact-size rejection</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Mounting an existing blob from a source repository into a destination repository without re-uploading it.">Cross-repository `Mount`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Mixed concurrent operations on one shared client complete correctly under the race detector.">Shared-client concurrency</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="No registry Authorization, cookie, or Referer header follows a redirect to off-origin blob storage.">Off-origin redirect credential scope</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Upload sessions follow relative and absolute Location URLs, preserving their opaque query state.">Upload `Location` handling</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Automatic retry after a 429 or 5xx response from the registry.">Retry after registry throttling</span> | :material-minus:{ .result-na title="N/A" } |

=== "Hub"

    Docker Hub.

    | Feature | Result |
    |---|:---:|
    | <span title="The registry rejects unauthenticated /v2/ requests and accepts the campaign credential over HTTPS.">HTTPS and authentication</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Push and pull of a roughly 1 KiB blob, verified byte-for-byte against independent controls.">Small blob (about 1 KiB)</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Exists returns true for a stored digest and false, without an error, for a missing one.">`Exists`, present and missing</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Single-stream download returning exact bytes and a digest-verified end of stream.">Serial `Pull`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Progress counts are monotonic, end at the exact byte total, and do not overlap within one transfer.">Progress reporting</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Ranged reads return exact byte windows; past-end and EOF-crossing ranges are rejected.">`PullRange`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Concurrent ranged workers reassemble the blob in order through the same digest-verifying reader.">Parallel `Pull`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="When a registry ignores ranged requests, parallel Pull falls back to a single stream.">Parallel range-ignored fallback</span> | :material-minus:{ .result-na title="N/A" } |
    | <span title="A connection broken mid-body resumes with a ranged request and still returns exact bytes.">Interrupted `Pull` resume</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="A pushed blob is retrievable before any manifest references it. Observed behavior, not a guarantee: a registry may garbage-collect unreferenced blobs at any time.">Unreferenced blob retrieval</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="The default single-PUT upload, independently verified after commit.">Monolithic `Push`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Uploading and retrieving the canonical zero-byte blob.">Empty blob `Push` and `Pull`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="The opt-in PATCH-chunked upload, committed and independently verified.">Chunked `Push`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="A commit under a digest that does not match the uploaded bytes is rejected, and neither digest becomes retrievable.">Wrong-digest rejection</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Readers yielding fewer or more bytes than the declared size are rejected without committing anything.">Exact-size rejection</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Mounting an existing blob from a source repository into a destination repository without re-uploading it.">Cross-repository `Mount`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Mixed concurrent operations on one shared client complete correctly under the race detector.">Shared-client concurrency</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="No registry Authorization, cookie, or Referer header follows a redirect to off-origin blob storage.">Off-origin redirect credential scope</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Upload sessions follow relative and absolute Location URLs, preserving their opaque query state.">Upload `Location` handling</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Automatic retry after a 429 or 5xx response from the registry.">Retry after registry throttling</span> | :material-minus:{ .result-na title="N/A" } |

=== "gcr.io"

    The `gcr.io` URL served by Google Artifact Registry.

    | Feature | Result |
    |---|:---:|
    | <span title="The registry rejects unauthenticated /v2/ requests and accepts the campaign credential over HTTPS.">HTTPS and authentication</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Push and pull of a roughly 1 KiB blob, verified byte-for-byte against independent controls.">Small blob (about 1 KiB)</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Exists returns true for a stored digest and false, without an error, for a missing one.">`Exists`, present and missing</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Single-stream download returning exact bytes and a digest-verified end of stream.">Serial `Pull`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Progress counts are monotonic, end at the exact byte total, and do not overlap within one transfer.">Progress reporting</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Ranged reads return exact byte windows; past-end and EOF-crossing ranges are rejected.">`PullRange`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Concurrent ranged workers reassemble the blob in order through the same digest-verifying reader.">Parallel `Pull`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="When a registry ignores ranged requests, parallel Pull falls back to a single stream.">Parallel range-ignored fallback</span> | :material-minus:{ .result-na title="N/A" } |
    | <span title="A connection broken mid-body resumes with a ranged request and still returns exact bytes.">Interrupted `Pull` resume</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="A pushed blob is retrievable before any manifest references it. Observed behavior, not a guarantee: a registry may garbage-collect unreferenced blobs at any time.">Unreferenced blob retrieval</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="The default single-PUT upload, independently verified after commit.">Monolithic `Push`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Uploading and retrieving the canonical zero-byte blob.">Empty blob `Push` and `Pull`</span> | :material-close:{ .result-no title="NO" } |
    | <span title="The opt-in PATCH-chunked upload, committed and independently verified.">Chunked `Push`</span> | :material-close:{ .result-no title="NO" } |
    | <span title="A commit under a digest that does not match the uploaded bytes is rejected, and neither digest becomes retrievable.">Wrong-digest rejection</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Readers yielding fewer or more bytes than the declared size are rejected without committing anything.">Exact-size rejection</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Mounting an existing blob from a source repository into a destination repository without re-uploading it.">Cross-repository `Mount`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Mixed concurrent operations on one shared client complete correctly under the race detector.">Shared-client concurrency</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="No registry Authorization, cookie, or Referer header follows a redirect to off-origin blob storage.">Off-origin redirect credential scope</span> | :material-minus:{ .result-na title="N/A" } |
    | <span title="Upload sessions follow relative and absolute Location URLs, preserving their opaque query state.">Upload `Location` handling</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Automatic retry after a 429 or 5xx response from the registry.">Retry after registry throttling</span> | :material-minus:{ .result-na title="N/A" } |

=== "Quay"

    Quay.io.

    | Feature | Result |
    |---|:---:|
    | <span title="The registry rejects unauthenticated /v2/ requests and accepts the campaign credential over HTTPS.">HTTPS and authentication</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Push and pull of a roughly 1 KiB blob, verified byte-for-byte against independent controls.">Small blob (about 1 KiB)</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Exists returns true for a stored digest and false, without an error, for a missing one.">`Exists`, present and missing</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Single-stream download returning exact bytes and a digest-verified end of stream.">Serial `Pull`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Progress counts are monotonic, end at the exact byte total, and do not overlap within one transfer.">Progress reporting</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Ranged reads return exact byte windows; past-end and EOF-crossing ranges are rejected.">`PullRange`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Concurrent ranged workers reassemble the blob in order through the same digest-verifying reader.">Parallel `Pull`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="When a registry ignores ranged requests, parallel Pull falls back to a single stream.">Parallel range-ignored fallback</span> | :material-minus:{ .result-na title="N/A" } |
    | <span title="A connection broken mid-body resumes with a ranged request and still returns exact bytes.">Interrupted `Pull` resume</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="A pushed blob is retrievable before any manifest references it. Observed behavior, not a guarantee: a registry may garbage-collect unreferenced blobs at any time.">Unreferenced blob retrieval</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="The default single-PUT upload, independently verified after commit.">Monolithic `Push`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Uploading and retrieving the canonical zero-byte blob.">Empty blob `Push` and `Pull`</span> | :material-close:{ .result-no title="NO" } |
    | <span title="The opt-in PATCH-chunked upload, committed and independently verified.">Chunked `Push`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="A commit under a digest that does not match the uploaded bytes is rejected, and neither digest becomes retrievable.">Wrong-digest rejection</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Readers yielding fewer or more bytes than the declared size are rejected without committing anything.">Exact-size rejection</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Mounting an existing blob from a source repository into a destination repository without re-uploading it.">Cross-repository `Mount`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Mixed concurrent operations on one shared client complete correctly under the race detector.">Shared-client concurrency</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="No registry Authorization, cookie, or Referer header follows a redirect to off-origin blob storage.">Off-origin redirect credential scope</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Upload sessions follow relative and absolute Location URLs, preserving their opaque query state.">Upload `Location` handling</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Automatic retry after a 429 or 5xx response from the registry.">Retry after registry throttling</span> | :material-minus:{ .result-na title="N/A" } |

=== "ACR"

    Azure Container Registry.

    | Feature | Result |
    |---|:---:|
    | <span title="The registry rejects unauthenticated /v2/ requests and accepts the campaign credential over HTTPS.">HTTPS and authentication</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Push and pull of a roughly 1 KiB blob, verified byte-for-byte against independent controls.">Small blob (about 1 KiB)</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Exists returns true for a stored digest and false, without an error, for a missing one.">`Exists`, present and missing</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Single-stream download returning exact bytes and a digest-verified end of stream.">Serial `Pull`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Progress counts are monotonic, end at the exact byte total, and do not overlap within one transfer.">Progress reporting</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Ranged reads return exact byte windows; past-end and EOF-crossing ranges are rejected.">`PullRange`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Concurrent ranged workers reassemble the blob in order through the same digest-verifying reader.">Parallel `Pull`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="When a registry ignores ranged requests, parallel Pull falls back to a single stream.">Parallel range-ignored fallback</span> | :material-minus:{ .result-na title="N/A" } |
    | <span title="A connection broken mid-body resumes with a ranged request and still returns exact bytes.">Interrupted `Pull` resume</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="A pushed blob is retrievable before any manifest references it. Observed behavior, not a guarantee: a registry may garbage-collect unreferenced blobs at any time.">Unreferenced blob retrieval</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="The default single-PUT upload, independently verified after commit.">Monolithic `Push`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Uploading and retrieving the canonical zero-byte blob.">Empty blob `Push` and `Pull`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="The opt-in PATCH-chunked upload, committed and independently verified.">Chunked `Push`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="A commit under a digest that does not match the uploaded bytes is rejected, and neither digest becomes retrievable.">Wrong-digest rejection</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Readers yielding fewer or more bytes than the declared size are rejected without committing anything.">Exact-size rejection</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Mounting an existing blob from a source repository into a destination repository without re-uploading it.">Cross-repository `Mount`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Mixed concurrent operations on one shared client complete correctly under the race detector.">Shared-client concurrency</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="No registry Authorization, cookie, or Referer header follows a redirect to off-origin blob storage.">Off-origin redirect credential scope</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Upload sessions follow relative and absolute Location URLs, preserving their opaque query state.">Upload `Location` handling</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Automatic retry after a 429 or 5xx response from the registry.">Retry after registry throttling</span> | :material-minus:{ .result-na title="N/A" } |

=== "Harbor"

    Harbor v2.15.2, self-hosted, filesystem storage.

    | Feature | Result |
    |---|:---:|
    | <span title="The registry rejects unauthenticated /v2/ requests and accepts the campaign credential over HTTPS.">HTTPS and authentication</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Push and pull of a roughly 1 KiB blob, verified byte-for-byte against independent controls.">Small blob (about 1 KiB)</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Exists returns true for a stored digest and false, without an error, for a missing one.">`Exists`, present and missing</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Single-stream download returning exact bytes and a digest-verified end of stream.">Serial `Pull`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Progress counts are monotonic, end at the exact byte total, and do not overlap within one transfer.">Progress reporting</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Ranged reads return exact byte windows; past-end and EOF-crossing ranges are rejected.">`PullRange`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Concurrent ranged workers reassemble the blob in order through the same digest-verifying reader.">Parallel `Pull`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="When a registry ignores ranged requests, parallel Pull falls back to a single stream.">Parallel range-ignored fallback</span> | :material-minus:{ .result-na title="N/A" } |
    | <span title="A connection broken mid-body resumes with a ranged request and still returns exact bytes.">Interrupted `Pull` resume</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="A pushed blob is retrievable before any manifest references it. Observed behavior, not a guarantee: a registry may garbage-collect unreferenced blobs at any time.">Unreferenced blob retrieval</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="The default single-PUT upload, independently verified after commit.">Monolithic `Push`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Uploading and retrieving the canonical zero-byte blob.">Empty blob `Push` and `Pull`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="The opt-in PATCH-chunked upload, committed and independently verified.">Chunked `Push`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="A commit under a digest that does not match the uploaded bytes is rejected, and neither digest becomes retrievable.">Wrong-digest rejection</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Readers yielding fewer or more bytes than the declared size are rejected without committing anything.">Exact-size rejection</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Mounting an existing blob from a source repository into a destination repository without re-uploading it.">Cross-repository `Mount`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Mixed concurrent operations on one shared client complete correctly under the race detector.">Shared-client concurrency</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="No registry Authorization, cookie, or Referer header follows a redirect to off-origin blob storage.">Off-origin redirect credential scope</span> | :material-minus:{ .result-na title="N/A" } |
    | <span title="Upload sessions follow relative and absolute Location URLs, preserving their opaque query state.">Upload `Location` handling</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Automatic retry after a 429 or 5xx response from the registry.">Retry after registry throttling</span> | :material-minus:{ .result-na title="N/A" } |

=== "GitLab"

    GitLab CE 19.2.1 native registry, self-hosted, filesystem storage.

    | Feature | Result |
    |---|:---:|
    | <span title="The registry rejects unauthenticated /v2/ requests and accepts the campaign credential over HTTPS.">HTTPS and authentication</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Push and pull of a roughly 1 KiB blob, verified byte-for-byte against independent controls.">Small blob (about 1 KiB)</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Exists returns true for a stored digest and false, without an error, for a missing one.">`Exists`, present and missing</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Single-stream download returning exact bytes and a digest-verified end of stream.">Serial `Pull`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Progress counts are monotonic, end at the exact byte total, and do not overlap within one transfer.">Progress reporting</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Ranged reads return exact byte windows; past-end and EOF-crossing ranges are rejected.">`PullRange`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Concurrent ranged workers reassemble the blob in order through the same digest-verifying reader.">Parallel `Pull`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="When a registry ignores ranged requests, parallel Pull falls back to a single stream.">Parallel range-ignored fallback</span> | :material-minus:{ .result-na title="N/A" } |
    | <span title="A connection broken mid-body resumes with a ranged request and still returns exact bytes.">Interrupted `Pull` resume</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="A pushed blob is retrievable before any manifest references it. Observed behavior, not a guarantee: a registry may garbage-collect unreferenced blobs at any time.">Unreferenced blob retrieval</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="The default single-PUT upload, independently verified after commit.">Monolithic `Push`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Uploading and retrieving the canonical zero-byte blob.">Empty blob `Push` and `Pull`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="The opt-in PATCH-chunked upload, committed and independently verified.">Chunked `Push`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="A commit under a digest that does not match the uploaded bytes is rejected, and neither digest becomes retrievable.">Wrong-digest rejection</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Readers yielding fewer or more bytes than the declared size are rejected without committing anything.">Exact-size rejection</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Mounting an existing blob from a source repository into a destination repository without re-uploading it.">Cross-repository `Mount`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Mixed concurrent operations on one shared client complete correctly under the race detector.">Shared-client concurrency</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="No registry Authorization, cookie, or Referer header follows a redirect to off-origin blob storage.">Off-origin redirect credential scope</span> | :material-minus:{ .result-na title="N/A" } |
    | <span title="Upload sessions follow relative and absolute Location URLs, preserving their opaque query state.">Upload `Location` handling</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Automatic retry after a 429 or 5xx response from the registry.">Retry after registry throttling</span> | :material-minus:{ .result-na title="N/A" } |

=== "Nexus"

    Sonatype Nexus Repository OSS 3.76.0, self-hosted, filesystem storage.

    | Feature | Result |
    |---|:---:|
    | <span title="The registry rejects unauthenticated /v2/ requests and accepts the campaign credential over HTTPS.">HTTPS and authentication</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Push and pull of a roughly 1 KiB blob, verified byte-for-byte against independent controls.">Small blob (about 1 KiB)</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Exists returns true for a stored digest and false, without an error, for a missing one.">`Exists`, present and missing</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Single-stream download returning exact bytes and a digest-verified end of stream.">Serial `Pull`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Progress counts are monotonic, end at the exact byte total, and do not overlap within one transfer.">Progress reporting</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Ranged reads return exact byte windows; past-end and EOF-crossing ranges are rejected.">`PullRange`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Concurrent ranged workers reassemble the blob in order through the same digest-verifying reader.">Parallel `Pull`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="When a registry ignores ranged requests, parallel Pull falls back to a single stream.">Parallel range-ignored fallback</span> | :material-minus:{ .result-na title="N/A" } |
    | <span title="A connection broken mid-body resumes with a ranged request and still returns exact bytes.">Interrupted `Pull` resume</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="A pushed blob is retrievable before any manifest references it. Observed behavior, not a guarantee: a registry may garbage-collect unreferenced blobs at any time.">Unreferenced blob retrieval</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="The default single-PUT upload, independently verified after commit.">Monolithic `Push`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Uploading and retrieving the canonical zero-byte blob.">Empty blob `Push` and `Pull`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="The opt-in PATCH-chunked upload, committed and independently verified.">Chunked `Push`</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="A commit under a digest that does not match the uploaded bytes is rejected, and neither digest becomes retrievable.">Wrong-digest rejection</span> | :material-close:{ .result-no title="NO" } |
    | <span title="Readers yielding fewer or more bytes than the declared size are rejected without committing anything.">Exact-size rejection</span> | :material-close:{ .result-no title="NO" } |
    | <span title="Mounting an existing blob from a source repository into a destination repository without re-uploading it.">Cross-repository `Mount`</span> | :material-close:{ .result-no title="NO" } |
    | <span title="Mixed concurrent operations on one shared client complete correctly under the race detector.">Shared-client concurrency</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="No registry Authorization, cookie, or Referer header follows a redirect to off-origin blob storage.">Off-origin redirect credential scope</span> | :material-minus:{ .result-na title="N/A" } |
    | <span title="Upload sessions follow relative and absolute Location URLs, preserving their opaque query state.">Upload `Location` handling</span> | :material-check:{ .result-pass title="PASS" } |
    | <span title="Automatic retry after a 429 or 5xx response from the registry.">Retry after registry throttling</span> | :material-minus:{ .result-na title="N/A" } |
