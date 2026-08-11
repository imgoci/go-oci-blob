package perf_test

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	blob "github.com/imgoci/go-oci-blob"
)

// pullEntireBlob drains and closes one verified pull.
func pullEntireBlob(
	t *testing.T, client *blob.Client, repo blob.Repository, dgst digest.Digest,
) {
	t.Helper()
	rc, err := client.Pull(t.Context(), repo, dgst)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
}

// TestParallelPullDefaultTransportReusesWorkerConnections verifies that
// repeated worker waves reuse the first pull's HTTP/1 connections instead of
// reopening the connections beyond net/http's default per-host idle limit.
func TestParallelPullDefaultTransportReusesWorkerConnections(t *testing.T) {
	const (
		workers   = 4
		chunkSize = 64 << 10
	)
	data := bytes.Repeat([]byte{'x'}, workers*chunkSize)
	dgst := digest.FromBytes(data)
	probeRange := "bytes=0-" + "65535"
	waveSize := int64(workers - 1)
	gates := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	var nonProbeRequests atomic.Int64
	var newConnections atomic.Int64

	path := "/v2/library/test/blobs/" + dgst.String()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Range") != probeRange {
			requestNumber := nonProbeRequests.Add(1)
			wave := (requestNumber - 1) / waveSize
			if wave >= int64(len(gates)) {
				http.Error(w, "unexpected range request", http.StatusInternalServerError)
				return
			}
			if requestNumber%waveSize == 0 {
				close(gates[wave])
			}
			select {
			case <-gates[wave]:
			case <-r.Context().Done():
				return
			}
		}
		http.ServeContent(w, r, "blob", time.Time{}, bytes.NewReader(data))
	}))
	server.EnableHTTP2 = false
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			newConnections.Add(1)
		}
	}
	server.Start()
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	repo := blob.Repository{Host: serverURL.Host, Name: "library/test"}
	client := blob.New(blob.WithPlainHTTP(true), blob.WithParallelPull(workers, chunkSize))

	for range gates {
		pullEntireBlob(t, client, repo, dgst)
	}
	assert.Equal(t, int64(len(gates))*waveSize, nonProbeRequests.Load())
	assert.Greater(t, newConnections.Load(), int64(http.DefaultMaxIdleConnsPerHost),
		"the test must exercise more connections than net/http retains by default")
	assert.LessOrEqual(t, newConnections.Load(), int64(workers),
		"repeated pulls should reuse the worker pool instead of accumulating connections")
}
