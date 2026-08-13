# go-oci-blob

go-oci-blob is a Go library that uploads and downloads OCI blobs. It covers
the blob subset of the OCI distribution spec and nothing else: push, pull,
existence checks, and cross-repository mounts, with retries and digest
verification built in.

```sh
go get github.com/imgoci/go-oci-blob
```

```go
client := blob.New(blob.WithTransport(authenticatedTransport))
repo := blob.Repository{Host: "registry.example.com", Name: "myorg/myrepo"}

dgst := digest.FromBytes(data)
err := client.Push(ctx, repo, dgst, int64(len(data)), bytes.NewReader(data))
```

Documentation:

- [Getting started tutorial](https://imgoci.github.io/go-oci-blob/tutorials/getting-started/)
- [API reference](https://pkg.go.dev/github.com/imgoci/go-oci-blob)
- [Registry compatibility](https://imgoci.github.io/go-oci-blob/reference/registry-compatibility/)
  — verified behavior across nine registries
- [Design](https://imgoci.github.io/go-oci-blob/explanation/design/)

Design constraints, in short:

- Runtime dependencies are the Go standard library plus
  `github.com/opencontainers/go-digest`.
- Authentication is the caller's job: inject an authenticated registry
  `http.RoundTripper`, for example from `oras-go` or `go-containerregistry`.
  Off-origin storage and CDN requests use a separate transport with registry
  credentials removed, so they do not follow an absolute upload location.
- Defaults use the code paths every registry serves correctly. Chunked upload
  and parallel pull exist behind explicit toggles.

## Development

[mise](https://mise.jdx.dev) provisions every pinned tool from `mise.toml` and
`mise.lock`: Go, Moon, Python and uv (for the docs site), and `golangci-lint`.
Run `mise install` once; there is nothing else to install by hand.

`mise install` runs with `locked = true`, so it fails closed if a tool lacks a
pre-resolved, checksummed entry for the current platform. To bump a tool, edit
its version in `mise.toml`, run
`mise lock --platform linux-x64,linux-arm64,macos-x64,macos-arm64`, and commit
`mise.toml` and `mise.lock` together.

Moon is the task front door:

```sh
moon run root:format
moon run root:lint
moon run root:build
moon run root:test
moon run root:check
```

CI runs the same aggregate check with `moon ci --summary minimal`.

## License

Licensed under either of

- Apache License, Version 2.0 ([LICENSE-APACHE](LICENSE-APACHE))
- MIT license ([LICENSE-MIT](LICENSE-MIT))

at your option.

Unless you explicitly state otherwise, any contribution intentionally
submitted for inclusion in this project by you, as defined in the Apache-2.0
license, shall be dual licensed as above, without any additional terms or
conditions.
