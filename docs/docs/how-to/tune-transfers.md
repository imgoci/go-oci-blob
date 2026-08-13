# How to enable chunked upload and parallel pull

Switch a client from the default monolithic upload or single-stream download
to chunked `Push` or parallel `Pull`. Both are client options, off by
default, because the defaults are the code paths every tested registry
serves correctly.

## Prerequisites

- A working client; see
  [How to authenticate to a registry](authenticate.md).
- For chunked upload: confirmation that your registry supports it. Check
  [Registry compatibility](../reference/registry-compatibility.md) first —
  major hosted registries fail it.

## Enable parallel pull

Pass worker count and chunk size when constructing the client:

```go
client := blob.New(
	blob.WithTransport(rt),
	blob.WithParallelPull(4, 1<<20), // 4 workers, 1 MiB chunks
)
```

`Pull` keeps its contract: one reader, bytes in order, digest verified. If
the registry does not serve ranged requests, `Pull` falls back to a single
stream, so this option is safe to set even when range support is unknown.

Pick the numbers by their memory cost: buffering is bounded by roughly
`workers × chunkSize` (the example above bounds it at 4 MiB). Four workers
with 1 MiB chunks is the configuration the compatibility campaigns ran
against all nine tested registries.

If you supply your own transport with `WithTransport`, size its connection
pool for the worker count; the library only tunes its own default transport:

```go
t := http.DefaultTransport.(*http.Transport).Clone()
t.MaxIdleConnsPerHost = workers
```

## Enable chunked upload

Pass the chunk size:

```go
client := blob.New(
	blob.WithTransport(rt),
	blob.WithChunkedUpload(1<<20), // 1 MiB PATCH chunks
)
```

Every `Push` on this client now uploads in `PATCH` chunks of that size and
commits with a final `PUT`. The client verifies the registry's `Range`
acknowledgement after every chunk and fails the upload rather than commit a
blob with silently dropped bytes.

Do not enable chunked upload against an unverified registry. Chunked upload
is optional in the OCI distribution spec and broken on major hosted
registries: in the 2026-08-12 compatibility campaign, Amazon ECR acknowledged
every chunk and never made the blob available, and `gcr.io` accepted the
first chunk and answered the next upload request with `405`. See
[Registry compatibility](../reference/registry-compatibility.md) for the
verified per-registry results.

## Verification

Transfer one blob and check it back independently:

1. `Push` a payload larger than one chunk.
2. `Pull` it with a second, default-configured client and compare bytes, or
   fetch it with another tool such as `oras blob fetch`.

For parallel pull, a successful `Pull` is sufficient: the returned reader
already verified the digest over the reassembled stream.

## Troubleshooting

### Chunked `Push` returns an error mid-upload

The registry did not acknowledge a chunk's byte range. The client abandons
the session on purpose instead of risking a corrupt commit. Use the default
monolithic upload for this registry.

### Parallel `Pull` is not faster

If the registry ignores ranges, `Pull` silently used a single stream. Check
the compatibility matrix, and confirm your transport's connection pool is
not capping concurrency (see above).

### The options appear to do nothing

Values below one are ignored and leave the default behavior in place, as are
worker counts above 1,024.

## Related

- [Registry compatibility](../reference/registry-compatibility.md) — which
  registries passed chunked and parallel transfers.
- [Design](../explanation/design.md) — why both features are opt-in.
