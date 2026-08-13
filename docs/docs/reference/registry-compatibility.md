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

## Result labels

| Label | Meaning |
|---|---|
| PASS | The feature worked and its result was independently verified. |
| NO | The registry and library combination did not support the feature. |
| N/A | The campaign did not exercise this path on this registry. |

## Differences at a glance

Every registry passed authenticated reads, ranged and parallel pulls, resume,
monolithic push, concurrency, and upload `Location` handling. The registries
differ only here:

- **Empty blob `Push` and `Pull`** — rejected or lost on ECR, GHCR, `gcr.io`,
  and Quay.io.
- **Chunked `Push`** — broken on ECR and `gcr.io`.
- **Wrong-digest rejection, exact-size rejection, and cross-repository
  `Mount`** — unsupported on Nexus Repository OSS 3.76.0.

Two paths produced no result anywhere: no registry ignored ranged requests
(the parallel fallback never triggered) and none answered with `429` or `5xx`
(retry after throttling was not observed).

## Results by registry

The footnoted row¹ is observed behavior, not a documented registry guarantee:
every registry served a pushed blob before any manifest referenced it, but a
registry may garbage-collect unreferenced blobs at any time.

=== "ECR"

    Amazon ECR Private.

    | Feature | Result |
    |---|---|
    | HTTPS and authentication | PASS |
    | Small blob (about 1 KiB) | PASS |
    | `Exists`, present and missing | PASS |
    | Serial `Pull` | PASS |
    | Progress reporting | PASS |
    | `PullRange` | PASS |
    | Parallel `Pull` | PASS |
    | Parallel range-ignored fallback | N/A |
    | Interrupted `Pull` resume | PASS |
    | Unreferenced blob retrieval¹ | PASS |
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

    **Empty blob Push and Pull**: The commit returned `400 BLOB_UPLOAD_INVALID` because the upload had no parts.

    **Chunked Push**: ECR advertised a 10 MiB minimum chunk length, acknowledged every `PATCH`, and never made any tested chunked blob available.

    **Cross-repository Mount**: Requires the regional registry's `BLOB_MOUNTING` setting to be `ENABLED`. With mounting disabled, ECR declines the mount and `Mount` returns `(false, nil)`.


=== "GHCR"

    GitHub Container Registry.

    | Feature | Result |
    |---|---|
    | HTTPS and authentication | PASS |
    | Small blob (about 1 KiB) | PASS |
    | `Exists`, present and missing | PASS |
    | Serial `Pull` | PASS |
    | Progress reporting | PASS |
    | `PullRange` | PASS |
    | Parallel `Pull` | PASS |
    | Parallel range-ignored fallback | N/A |
    | Interrupted `Pull` resume | PASS |
    | Unreferenced blob retrieval¹ | PASS |
    | Monolithic `Push` | PASS |
    | Empty blob `Push` and `Pull` | NO |
    | Chunked `Push` | PASS |
    | Wrong-digest rejection | PASS |
    | Exact-size rejection | PASS |
    | Cross-repository `Mount` | PASS |
    | Shared-client concurrency | PASS |
    | Off-origin redirect credential scope | PASS |
    | Upload `Location` handling | PASS |
    | Retry after registry throttling | N/A |

    **Empty blob Push and Pull**: `HEAD` reported the canonical empty digest as present, but a zero-byte upload returned `404 BLOB_UNKNOWN`.


=== "Hub"

    Docker Hub.

    | Feature | Result |
    |---|---|
    | HTTPS and authentication | PASS |
    | Small blob (about 1 KiB) | PASS |
    | `Exists`, present and missing | PASS |
    | Serial `Pull` | PASS |
    | Progress reporting | PASS |
    | `PullRange` | PASS |
    | Parallel `Pull` | PASS |
    | Parallel range-ignored fallback | N/A |
    | Interrupted `Pull` resume | PASS |
    | Unreferenced blob retrieval¹ | PASS |
    | Monolithic `Push` | PASS |
    | Empty blob `Push` and `Pull` | PASS |
    | Chunked `Push` | PASS |
    | Wrong-digest rejection | PASS |
    | Exact-size rejection | PASS |
    | Cross-repository `Mount` | PASS |
    | Shared-client concurrency | PASS |
    | Off-origin redirect credential scope | PASS |
    | Upload `Location` handling | PASS |
    | Retry after registry throttling | N/A |

=== "gcr.io"

    The `gcr.io` URL served by Google Artifact Registry.

    | Feature | Result |
    |---|---|
    | HTTPS and authentication | PASS |
    | Small blob (about 1 KiB) | PASS |
    | `Exists`, present and missing | PASS |
    | Serial `Pull` | PASS |
    | Progress reporting | PASS |
    | `PullRange` | PASS |
    | Parallel `Pull` | PASS |
    | Parallel range-ignored fallback | N/A |
    | Interrupted `Pull` resume | PASS |
    | Unreferenced blob retrieval¹ | PASS |
    | Monolithic `Push` | PASS |
    | Empty blob `Push` and `Pull` | NO |
    | Chunked `Push` | NO |
    | Wrong-digest rejection | PASS |
    | Exact-size rejection | PASS |
    | Cross-repository `Mount` | PASS |
    | Shared-client concurrency | PASS |
    | Off-origin redirect credential scope | N/A |
    | Upload `Location` handling | PASS |
    | Retry after registry throttling | N/A |

    **Empty blob Push and Pull**: The zero-byte commit returned `400 Bad Request`.

    **Chunked Push**: The registry accepted the first `PATCH` with `202` and answered the next upload request with `405 Method Not Allowed`.


=== "Quay"

    Quay.io.

    | Feature | Result |
    |---|---|
    | HTTPS and authentication | PASS |
    | Small blob (about 1 KiB) | PASS |
    | `Exists`, present and missing | PASS |
    | Serial `Pull` | PASS |
    | Progress reporting | PASS |
    | `PullRange` | PASS |
    | Parallel `Pull` | PASS |
    | Parallel range-ignored fallback | N/A |
    | Interrupted `Pull` resume | PASS |
    | Unreferenced blob retrieval¹ | PASS |
    | Monolithic `Push` | PASS |
    | Empty blob `Push` and `Pull` | NO |
    | Chunked `Push` | PASS |
    | Wrong-digest rejection | PASS |
    | Exact-size rejection | PASS |
    | Cross-repository `Mount` | PASS |
    | Shared-client concurrency | PASS |
    | Off-origin redirect credential scope | PASS |
    | Upload `Location` handling | PASS |
    | Retry after registry throttling | N/A |

    **Empty blob Push and Pull**: The zero-byte `Push` succeeded and `Exists` reported true, but every retrieval — library `Pull`, raw `GET`, and ORAS — returned `404`.


=== "ACR"

    Azure Container Registry.

    | Feature | Result |
    |---|---|
    | HTTPS and authentication | PASS |
    | Small blob (about 1 KiB) | PASS |
    | `Exists`, present and missing | PASS |
    | Serial `Pull` | PASS |
    | Progress reporting | PASS |
    | `PullRange` | PASS |
    | Parallel `Pull` | PASS |
    | Parallel range-ignored fallback | N/A |
    | Interrupted `Pull` resume | PASS |
    | Unreferenced blob retrieval¹ | PASS |
    | Monolithic `Push` | PASS |
    | Empty blob `Push` and `Pull` | PASS |
    | Chunked `Push` | PASS |
    | Wrong-digest rejection | PASS |
    | Exact-size rejection | PASS |
    | Cross-repository `Mount` | PASS |
    | Shared-client concurrency | PASS |
    | Off-origin redirect credential scope | PASS |
    | Upload `Location` handling | PASS |
    | Retry after registry throttling | N/A |

=== "Harbor"

    Harbor v2.15.2, self-hosted, filesystem storage.

    | Feature | Result |
    |---|---|
    | HTTPS and authentication | PASS |
    | Small blob (about 1 KiB) | PASS |
    | `Exists`, present and missing | PASS |
    | Serial `Pull` | PASS |
    | Progress reporting | PASS |
    | `PullRange` | PASS |
    | Parallel `Pull` | PASS |
    | Parallel range-ignored fallback | N/A |
    | Interrupted `Pull` resume | PASS |
    | Unreferenced blob retrieval¹ | PASS |
    | Monolithic `Push` | PASS |
    | Empty blob `Push` and `Pull` | PASS |
    | Chunked `Push` | PASS |
    | Wrong-digest rejection | PASS |
    | Exact-size rejection | PASS |
    | Cross-repository `Mount` | PASS |
    | Shared-client concurrency | PASS |
    | Off-origin redirect credential scope | N/A |
    | Upload `Location` handling | PASS |
    | Retry after registry throttling | N/A |

=== "GitLab"

    GitLab CE 19.2.1 native registry, self-hosted, filesystem storage.

    | Feature | Result |
    |---|---|
    | HTTPS and authentication | PASS |
    | Small blob (about 1 KiB) | PASS |
    | `Exists`, present and missing | PASS |
    | Serial `Pull` | PASS |
    | Progress reporting | PASS |
    | `PullRange` | PASS |
    | Parallel `Pull` | PASS |
    | Parallel range-ignored fallback | N/A |
    | Interrupted `Pull` resume | PASS |
    | Unreferenced blob retrieval¹ | PASS |
    | Monolithic `Push` | PASS |
    | Empty blob `Push` and `Pull` | PASS |
    | Chunked `Push` | PASS |
    | Wrong-digest rejection | PASS |
    | Exact-size rejection | PASS |
    | Cross-repository `Mount` | PASS |
    | Shared-client concurrency | PASS |
    | Off-origin redirect credential scope | N/A |
    | Upload `Location` handling | PASS |
    | Retry after registry throttling | N/A |

=== "Nexus"

    Sonatype Nexus Repository OSS 3.76.0, self-hosted, filesystem storage.

    | Feature | Result |
    |---|---|
    | HTTPS and authentication | PASS |
    | Small blob (about 1 KiB) | PASS |
    | `Exists`, present and missing | PASS |
    | Serial `Pull` | PASS |
    | Progress reporting | PASS |
    | `PullRange` | PASS |
    | Parallel `Pull` | PASS |
    | Parallel range-ignored fallback | N/A |
    | Interrupted `Pull` resume | PASS |
    | Unreferenced blob retrieval¹ | PASS |
    | Monolithic `Push` | PASS |
    | Empty blob `Push` and `Pull` | PASS |
    | Chunked `Push` | PASS |
    | Wrong-digest rejection | NO |
    | Exact-size rejection | NO |
    | Cross-repository `Mount` | NO |
    | Shared-client concurrency | PASS |
    | Off-origin redirect credential scope | N/A |
    | Upload `Location` handling | PASS |
    | Retry after registry throttling | N/A |

    **Wrong-digest rejection**: The commit returned `400` and `Push` returned an error, but the claimed digest afterwards answered `HEAD 200` and served bytes whose SHA-256 differs from it. A verified library `Pull` of that digest returns `ErrDigestMismatch`.

    **Exact-size rejection**: A reader with trailing data was rejected, but in three of five campaigns the declared prefix was committed anyway.

    **Cross-repository Mount**: The mount request between two hosted Docker repositories returned `202`; `Mount` returns `(false, nil)` and the caller falls back to `Push`.

