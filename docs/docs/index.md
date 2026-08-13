---
title: go-oci-blob
description: Go library for uploading and downloading OCI blobs.
---

# go-oci-blob

go-oci-blob is a Go library that uploads and downloads OCI blobs. It covers
the blob subset of the OCI distribution spec and nothing else: push, pull,
existence checks, and cross-repository mounts, with retries and digest
verification built in.

```sh
go get github.com/imgoci/go-oci-blob
```

The runtime dependencies are the Go standard library plus
`github.com/opencontainers/go-digest`. Authentication is the caller's job:
inject an authenticated `http.RoundTripper`.

## Documentation

**Start here**

- [Push and pull your first blob](tutorials/getting-started.md) — a hands-on
  introduction against a local registry.

**Solve a problem**

- [How to authenticate to a registry](how-to/authenticate.md)
- [How to enable chunked upload and parallel pull](how-to/tune-transfers.md)
- [Refresh the registry compatibility matrix](how-to/refresh-registry-compatibility.md)
  — maintainer procedure behind the compatibility reference.

**Look up facts**

- [API reference on pkg.go.dev](https://pkg.go.dev/github.com/imgoci/go-oci-blob)
  — the complete API contract lives in the package documentation.
- [Embedding API](reference/embedding.md) — one-attempt retries, error
  inspection, wire progress, redirect policy, and caller-owned boundaries.
- [Registry compatibility](reference/registry-compatibility.md) — verified
  behavior across nine registries.

**Understand the design**

- [Design](explanation/design.md) — scope, architecture, wire behavior, and
  the reasoning behind the API.
