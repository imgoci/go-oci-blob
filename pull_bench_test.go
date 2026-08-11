package blob_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"

	blob "github.com/imgoci/go-oci-blob"
)

// benchRegistry serves one blob with full Range support (via
// [http.ServeContent]), which is all Pull needs from a registry.
func benchRegistry(b *testing.B, data []byte, dgst digest.Digest) blob.Repository {
	b.Helper()

	path := "/v2/bench/blob/blobs/" + dgst.String()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, "blob", time.Time{}, bytes.NewReader(data))
	}))
	b.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	if err != nil {
		b.Fatal(err)
	}
	return blob.Repository{Host: serverURL.Host, Name: "bench/blob"}
}

// benchmarkPull measures one full verified pull per iteration.
func benchmarkPull(b *testing.B, client *blob.Client, repo blob.Repository, dgst digest.Digest, size int64) {
	b.Helper()
	b.SetBytes(size)
	b.ReportAllocs()

	for b.Loop() {
		rc, err := client.Pull(b.Context(), repo, dgst)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, rc); err != nil {
			b.Fatal(err)
		}
		if err := rc.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPull(b *testing.B) {
	data := bytes.Repeat([]byte("benchmarking blob transfer bytes"), 1<<19) // 16 MiB
	dgst := digest.FromBytes(data)
	size := int64(len(data))

	b.Run("single-stream", func(b *testing.B) {
		repo := benchRegistry(b, data, dgst)
		client := blob.New(blob.WithPlainHTTP(true))
		benchmarkPull(b, client, repo, dgst, size)
	})

	b.Run("parallel-4x1MiB", func(b *testing.B) {
		repo := benchRegistry(b, data, dgst)
		client := blob.New(blob.WithPlainHTTP(true), blob.WithParallelPull(4, 1<<20))
		benchmarkPull(b, client, repo, dgst, size)
	})

	b.Run("parallel-8x1MiB", func(b *testing.B) {
		repo := benchRegistry(b, data, dgst)
		client := blob.New(blob.WithPlainHTTP(true), blob.WithParallelPull(8, 1<<20))
		benchmarkPull(b, client, repo, dgst, size)
	})
}
