# Embedding API reference

This page describes the controls intended for applications that own retry scheduling, authentication, destination policy, progress aggregation, and public error mapping.

## One-attempt clients

`RetryPolicy{}` selects one request attempt and no retry delay. `New` uses `DefaultRetryPolicy()` only when the caller does not supply `WithRetryPolicy`.

```go
client := blob.New(
    blob.WithTransport(authenticatedRegistryTransport),
    blob.WithStorageTransport(guardedStorageTransport),
    blob.WithRetryPolicy(blob.RetryPolicy{}),
    blob.WithWriteRedirects(false),
)
```

A zero-value policy changes only the retry count. It does not change which failures `Retryable` classifies as suitable for a fresh operation.

## Failure inspection

```go
func Retryable(err error) (after time.Duration, ok bool)
func StatusCode(err error) (code int, ok bool)
```

`Retryable` returns `ok == true` when a fresh operation may succeed. `after` is the peer's usable `Retry-After` floor, or zero when the response supplied no usable delay. The classification and delay survive error wrapping and exhaustion of the client's retry policy, including a one-attempt policy.

The following failures are retryable:

- connection failures and request timeouts while the caller's context remains active;
- registry `429` and `5xx` responses; and
- off-origin storage `401`, `403`, `404`, and `410` responses.

Caller cancellation, caller deadline expiration, source-reader failures, invalid source sizes, invalid upload locations, redirect-policy failures, digest mismatches, and other terminal `4xx` responses are not retryable.

`StatusCode` returns the retained HTTP status for registry-origin and off-origin storage responses. Use `errors.Is` for stable high-level conditions:

| Error | Matching response |
|---|---|
| `ErrUnauthorized` | Registry-origin `401` or `403` |
| `ErrTooLarge` | Registry-origin `413` |
| `ErrNotFound` | Registry-origin `404` when the operation returns absence as an error |
| `ErrDigestMismatch` | Transferred bytes do not match the expected digest |

Off-origin storage `401` and `403` do not match `ErrUnauthorized`. Off-origin storage `404` does not match `ErrNotFound`. Rendered errors omit registry response details and upload-location values; inspect errors programmatically instead of parsing their text.

## Progress callbacks

`WithProgress` and `WithWireProgress` report different boundaries.

```go
func WithProgress(fn func(done, total int64)) TransferOption
func WithWireProgress(fn func(delta int64)) TransferOption
```

`WithProgress` reports cumulative committed transfer progress. Pull counts bytes delivered to the caller. Monolithic Push reports after the final `201 Created`. Chunked Push advances after each acknowledged `PATCH`; only a nil Push error proves that the final commit succeeded.

`WithWireProgress` reports positive upload-byte deltas when the HTTP transport consumes a request body. It does not count bytes staged by source read-ahead. Failed attempts, method-preserving redirects, and transparent retries contribute to the total because each consumed boundary traffic. A zero-length body reports nothing.

Calls to either callback are serialized within one transfer and stop before that transfer returns. Concurrent transfers may call the same callback concurrently. Callbacks run synchronously on the transfer path and must return quickly.

## Write redirects

`WithWriteRedirects(false)` rejects redirects that would reissue `POST`, `PUT`, `PATCH`, or `DELETE`. The client rejects the redirect before sending its target request and treats the failure as terminal. The rendered error does not contain the peer-selected redirect target.

The default is `WithWriteRedirects(true)`, which preserves method-preserving write redirects. Changing this option does not reject relative or absolute upload-session `Location` values returned by a successful registry response; those values are protocol state, not HTTP redirects.

## Caller-owned boundaries

`WithTransport` accepts the transport for the registry origin. The caller supplies registry authentication and credential policy.

`WithStorageTransport` accepts the transport for off-origin storage and CDN requests. The client removes registry credentials, cookies, proxy credentials, and referrer data before routing those requests. The caller supplies private-network, actual-peer, TLS, proxy, and storage-authentication policy. The package does not store credentials or block destination address ranges.
