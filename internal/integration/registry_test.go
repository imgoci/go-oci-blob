//go:build e2e

package integration

// Integration tests exercise real registries through testcontainers. They
// need a running Docker daemon and only build with the e2e tag:
// `go test -tags e2e ./internal/integration/...`.

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	blob "github.com/imgoci/go-oci-blob"
)

// testRegistries lists the registry images every integration test runs against.
// Both are OCI-conformant, multi-arch, and serve the distribution API
// on port 5000 with their stock configuration.
func testRegistries() []struct{ name, image string } {
	return []struct{ name, image string }{
		{name: "registry", image: "registry:2"},
		{name: "zot", image: "ghcr.io/project-zot/zot:v2.1.20"},
	}
}

// startRegistry launches a registry container and returns its
// host:port address, cleaned up with the test.
func startRegistry(t *testing.T, image string) string {
	t.Helper()
	ctx := t.Context()

	container, err := testcontainers.Run(ctx, image,
		testcontainers.WithExposedPorts("5000/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP("/v2/").WithPort("5000/tcp").
				WithStatusCodeMatcher(func(status int) bool { return status == http.StatusOK }),
		),
	)
	testcontainers.CleanupContainer(t, container)
	require.NoError(t, err, "starting %s container", image)

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "5000")
	require.NoError(t, err)

	return net.JoinHostPort(host, port.Port())
}

// seedBlob uploads data into the registry with a raw monolithic
// POST+PUT so tests can exercise read paths before Push exists.
func seedBlob(t *testing.T, registry, name string, dgst digest.Digest, data []byte) {
	t.Helper()
	ctx := t.Context()

	postURL := fmt.Sprintf("http://%s/v2/%s/blobs/uploads/", registry, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusAccepted, resp.StatusCode, "starting upload session")

	base, err := url.Parse(postURL)
	require.NoError(t, err)
	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err, "parsing upload Location %q", resp.Header.Get("Location"))
	putURL := base.ResolveReference(loc)
	query := putURL.Query()
	query.Set("digest", dgst.String())
	putURL.RawQuery = query.Encode()

	req, err = http.NewRequestWithContext(ctx, http.MethodPut, putURL.String(), bytes.NewReader(data))
	require.NoError(t, err)
	req.ContentLength = int64(len(data))
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusCreated, resp.StatusCode, "committing upload")
}

func TestExists(t *testing.T) {
	for _, reg := range testRegistries() {
		t.Run(reg.name, func(t *testing.T) {
			address := startRegistry(t, reg.image)
			repo := blob.Repository{Host: address, Name: "e2e/exists"}
			client := blob.New(blob.WithPlainHTTP(true))

			data := []byte("go-oci-blob e2e test blob")
			dgst := digest.FromBytes(data)
			seedBlob(t, address, repo.Name, dgst, data)

			exists, err := client.Exists(t.Context(), repo, dgst)
			require.NoError(t, err)
			assert.True(t, exists, "seeded blob should exist")

			missing, err := client.Exists(t.Context(), repo, digest.FromString("not there"))
			require.NoError(t, err)
			assert.False(t, missing, "unseeded digest should not exist")
		})
	}
}

func TestPull(t *testing.T) {
	for _, reg := range testRegistries() {
		t.Run(reg.name, func(t *testing.T) {
			address := startRegistry(t, reg.image)
			repo := blob.Repository{Host: address, Name: "e2e/pull"}
			client := blob.New(blob.WithPlainHTTP(true))

			data := bytes.Repeat([]byte("go-oci-blob e2e pull round-trip "), 1024)
			dgst := digest.FromBytes(data)
			seedBlob(t, address, repo.Name, dgst, data)

			rc, err := client.Pull(t.Context(), repo, dgst)
			require.NoError(t, err)
			got, err := io.ReadAll(rc)
			require.NoError(t, err, "reading to EOF verifies the digest")
			require.NoError(t, rc.Close())
			assert.Equal(t, data, got, "pulled bytes should round-trip")

			_, err = client.Pull(t.Context(), repo, digest.FromString("not there"))
			require.ErrorIs(t, err, blob.ErrNotFound)
		})
	}
}

func TestPullRange(t *testing.T) {
	for _, reg := range testRegistries() {
		t.Run(reg.name, func(t *testing.T) {
			address := startRegistry(t, reg.image)
			repo := blob.Repository{Host: address, Name: "e2e/pullrange"}
			client := blob.New(blob.WithPlainHTTP(true))

			data := bytes.Repeat([]byte("0123456789"), 100)
			dgst := digest.FromBytes(data)
			seedBlob(t, address, repo.Name, dgst, data)

			const offset, length = 250, 500
			rc, err := client.PullRange(t.Context(), repo, dgst, offset, length)
			require.NoError(t, err)
			got, err := io.ReadAll(rc)
			require.NoError(t, err)
			require.NoError(t, rc.Close())
			assert.Equal(t, data[offset:offset+length], got,
				"ranged pull should serve exactly the requested window")
		})
	}
}

func TestPush(t *testing.T) {
	for _, reg := range testRegistries() {
		t.Run(reg.name, func(t *testing.T) {
			address := startRegistry(t, reg.image)
			repo := blob.Repository{Host: address, Name: "e2e/push"}
			client := blob.New(blob.WithPlainHTTP(true))

			data := bytes.Repeat([]byte("go-oci-blob e2e push round-trip "), 2048)
			dgst := digest.FromBytes(data)

			err := client.Push(t.Context(), repo, dgst, int64(len(data)), bytes.NewReader(data))
			require.NoError(t, err)

			exists, err := client.Exists(t.Context(), repo, dgst)
			require.NoError(t, err)
			assert.True(t, exists, "pushed blob should exist")

			rc, err := client.Pull(t.Context(), repo, dgst)
			require.NoError(t, err)
			got, err := io.ReadAll(rc)
			require.NoError(t, err, "reading to EOF verifies the digest")
			require.NoError(t, rc.Close())
			assert.Equal(t, data, got, "pushed bytes should pull back unchanged")
		})
	}
}

func TestParallelPull(t *testing.T) {
	for _, reg := range testRegistries() {
		t.Run(reg.name, func(t *testing.T) {
			address := startRegistry(t, reg.image)
			repo := blob.Repository{Host: address, Name: "e2e/parallel"}
			client := blob.New(
				blob.WithPlainHTTP(true),
				blob.WithParallelPull(4, 128<<10),
			)

			data := bytes.Repeat([]byte("parallel pull e2e"), 65_000) // ~1.1 MiB → 9 chunks
			dgst := digest.FromBytes(data)
			seedBlob(t, address, repo.Name, dgst, data)

			rc, err := client.Pull(t.Context(), repo, dgst)
			require.NoError(t, err)
			got, err := io.ReadAll(rc)
			require.NoError(t, err, "reading to EOF verifies the digest")
			require.NoError(t, rc.Close())
			assert.Equal(t, data, got, "parallel pull should reassemble the blob in order")
		})
	}
}

func TestChunkedPush(t *testing.T) {
	for _, reg := range testRegistries() {
		t.Run(reg.name, func(t *testing.T) {
			address := startRegistry(t, reg.image)
			repo := blob.Repository{Host: address, Name: "e2e/chunked"}
			client := blob.New(
				blob.WithPlainHTTP(true),
				blob.WithChunkedUpload(64<<10),
			)

			data := bytes.Repeat([]byte("chunked e2e"), 15_000) // ~165 KiB → 3 chunks
			dgst := digest.FromBytes(data)

			err := client.Push(t.Context(), repo, dgst, int64(len(data)), bytes.NewReader(data))
			require.NoError(t, err)

			rc, err := client.Pull(t.Context(), repo, dgst)
			require.NoError(t, err)
			got, err := io.ReadAll(rc)
			require.NoError(t, err, "reading to EOF verifies the digest")
			require.NoError(t, rc.Close())
			assert.Equal(t, data, got, "chunked-pushed bytes should pull back unchanged")
		})
	}
}

func TestMount(t *testing.T) {
	for _, reg := range testRegistries() {
		t.Run(reg.name, func(t *testing.T) {
			address := startRegistry(t, reg.image)
			src := blob.Repository{Host: address, Name: "e2e/mount-src"}
			dst := blob.Repository{Host: address, Name: "e2e/mount-dst"}
			client := blob.New(blob.WithPlainHTTP(true))

			data := []byte("go-oci-blob e2e mount blob")
			dgst := digest.FromBytes(data)
			require.NoError(t,
				client.Push(t.Context(), src, dgst, int64(len(data)), bytes.NewReader(data)))

			mounted, err := client.Mount(t.Context(), dst, src, dgst)
			require.NoError(t, err)
			assert.True(t, mounted, "registry should mount the blob across repositories")

			exists, err := client.Exists(t.Context(), dst, dgst)
			require.NoError(t, err)
			assert.True(t, exists, "mounted blob should exist in the destination")
		})
	}
}
