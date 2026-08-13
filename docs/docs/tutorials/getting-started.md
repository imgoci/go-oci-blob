# Push and pull your first blob

In this tutorial, we will push a blob to a container registry running on your
machine, confirm the registry has it, and pull it back with digest
verification. By the end you will have used the library's three core
operations from a small Go program you wrote yourself.

## Prerequisites

- [Go](https://go.dev/doc/install) 1.26 or later
- [Docker](https://docs.docker.com/get-docker/)

No registry account is needed. Everything runs locally and is deleted at the
end.

## Step 1: Start a local registry

We need a registry to talk to. Run one in Docker:

```sh
docker run -d --name blob-tutorial -p 127.0.0.1:5001:5000 registry:2
```

Confirm it answers:

```sh
curl http://localhost:5001/v2/
```

You should see:

```text
{}
```

That empty JSON object is the registry saying it speaks the OCI distribution
API.

## Step 2: Create a Go program

Create a new directory and module:

```sh
mkdir blob-tutorial && cd blob-tutorial
go mod init blobtutorial
go get github.com/imgoci/go-oci-blob
```

Create `main.go` with exactly this content:

```go
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"

	blob "github.com/imgoci/go-oci-blob"
	"github.com/opencontainers/go-digest"
)

func main() {
	ctx := context.Background()

	client := blob.New(blob.WithPlainHTTP(true))
	repo := blob.Repository{Host: "localhost:5001", Name: "tutorial/hello"}

	data := []byte("Hello, OCI!\n")
	dgst := digest.FromBytes(data)
	fmt.Println("digest:", dgst)

	// Push the blob.
	err := client.Push(ctx, repo, dgst, int64(len(data)), bytes.NewReader(data))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("pushed", len(data), "bytes")

	// Ask the registry whether it has the blob now.
	ok, err := client.Exists(ctx, repo, dgst)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("exists:", ok)

	// Pull it back. The reader verifies the digest as bytes flow.
	rc, err := client.Pull(ctx, repo, dgst)
	if err != nil {
		log.Fatal(err)
	}
	defer rc.Close()

	pulled, err := io.ReadAll(rc)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("pulled: %q\n", pulled)
}
```

## Step 3: Run it

```sh
go run .
```

You should see:

```text
digest: sha256:9e9181d7aef394d410d7442402afd34d6865e4b7ca1825b9f3d442b3ec0df766
pushed 12 bytes
exists: true
pulled: "Hello, OCI!\n"
```

Three things happened against a real registry: `Push` uploaded the bytes under
their SHA-256 digest, `Exists` confirmed the registry stored them, and `Pull`
streamed them back.

## Step 4: Ask for a blob the registry does not have

Every blob is addressed by the digest of its content. Let's see what happens
when we ask for one the registry never stored.

In `main.go`, find the `Pull` call:

```go
	rc, err := client.Pull(ctx, repo, dgst)
```

and replace its digest argument with the digest of different content — the
empty blob:

```go
	rc, err := client.Pull(ctx, repo, digest.FromBytes(nil))
```

Run it again:

```sh
go run .
```

You should see the program fail:

```text
digest: sha256:9e9181d7aef394d410d7442402afd34d6865e4b7ca1825b9f3d442b3ec0df766
pushed 12 bytes
exists: true
2026/08/12 20:13:11 pulling blob sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855 from localhost:5001/tutorial/hello: registry returned 404: BLOB_UNKNOWN: blob unknown to registry
```

The registry never stored a blob under that digest, so `Pull` failed with an
error matching `blob.ErrNotFound` before any bytes flowed. (The timestamp
comes from `log.Fatal` in our program; yours will differ.)

Restore the original line before moving on.

## Step 5: Clean up

Remove the registry container:

```sh
docker rm -f blob-tutorial
```

## What we learned

- `blob.New` builds a client; options such as `WithPlainHTTP` configure it.
- A `blob.Repository` is a host plus a repository name.
- `Push` uploads bytes under their digest; `Exists` and `Pull` find them
  again.
- `Pull` verifies content against the digest you asked for; a digest the
  registry does not have is `ErrNotFound`.

## Next steps

- [How to authenticate to a registry](../how-to/authenticate.md) — real
  registries need credentials.
- [Registry compatibility](../reference/registry-compatibility.md) — what
  nine real registries were verified to support.
- [Design](../explanation/design.md) — why the API looks the way it does.
