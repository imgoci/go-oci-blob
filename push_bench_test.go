package blob_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/opencontainers/go-digest"

	blob "github.com/imgoci/go-oci-blob"
)

const pushBenchmarkSize = 16 << 20

// pushBenchmarkRegistry accepts monolithic and verified chunked uploads while
// discarding their bytes, keeping the benchmark focused on the transfer path.
func pushBenchmarkRegistry(b *testing.B) (blob.Repository, *atomic.Int64) {
	b.Helper()

	var received atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case http.MethodPost:
			w.Header().Set("Location", "/upload/1")
			w.WriteHeader(http.StatusAccepted)
		case http.MethodPatch:
			n, err := io.Copy(io.Discard, req.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			end := received.Add(n) - 1
			w.Header().Set("Range", fmt.Sprintf("0-%d", end))
			w.WriteHeader(http.StatusAccepted)
		case http.MethodPut:
			n, err := io.Copy(io.Discard, req.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if n != req.ContentLength {
				http.Error(w, "request body did not match Content-Length", http.StatusBadRequest)
				return
			}
			received.Add(n)
			w.WriteHeader(http.StatusCreated)
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	b.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	if err != nil {
		b.Fatal(err)
	}
	return blob.Repository{Host: serverURL.Host, Name: "bench/blob"}, &received
}

// benchmarkPush measures one complete upload per iteration.
func benchmarkPush(
	b *testing.B,
	client *blob.Client,
	repo blob.Repository,
	received *atomic.Int64,
	data []byte,
	dgst digest.Digest,
) {
	b.Helper()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()

	for b.Loop() {
		received.Store(0)
		err := client.Push(
			b.Context(), repo, dgst, int64(len(data)), bytes.NewReader(data))
		if err != nil {
			b.Fatal(err)
		}
		if got := received.Load(); got != int64(len(data)) {
			b.Fatalf("registry received %d bytes, want %d", got, len(data))
		}
	}
}

func BenchmarkPush(b *testing.B) {
	data := bytes.Repeat([]byte("upload-benchmark"),
		pushBenchmarkSize/len("upload-benchmark")+1)[:pushBenchmarkSize]
	dgst := digest.FromBytes(data)

	b.Run("monolithic", func(b *testing.B) {
		repo, received := pushBenchmarkRegistry(b)
		client := blob.New(blob.WithPlainHTTP(true))
		benchmarkPush(b, client, repo, received, data, dgst)
	})

	b.Run("chunked-1MiB", func(b *testing.B) {
		repo, received := pushBenchmarkRegistry(b)
		client := blob.New(blob.WithPlainHTTP(true), blob.WithChunkedUpload(1<<20))
		benchmarkPush(b, client, repo, received, data, dgst)
	})

	b.Run("small-concurrent-1KiB", func(b *testing.B) {
		const smallSize = 1 << 10
		smallData := bytes.Repeat([]byte("s"), smallSize)
		smallDigest := digest.FromBytes(smallData)
		repo, _ := pushBenchmarkRegistry(b)
		client := blob.New(blob.WithPlainHTTP(true))
		b.SetBytes(smallSize)
		b.ReportAllocs()

		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				err := client.Push(
					b.Context(), repo, smallDigest, smallSize, bytes.NewReader(smallData))
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	})
}
