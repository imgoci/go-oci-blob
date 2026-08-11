package blob

import (
	"bytes"
	"io"
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stagedUploadCapacity reports the buffers owned by body while it is active.
func stagedUploadCapacity(body *uploadBody) (int, int) {
	body.stateMu.Lock()
	defer body.stateMu.Unlock()
	capacity := 0
	for i := range body.bufferCount {
		capacity += cap(body.buffers[i])
	}
	return body.bufferCount, capacity
}

// TestUploadBodyStagesProportionallyToExpectedSize verifies tiny uploads do not
// pay the full large-transfer buffering bound and huge sizes cannot overflow.
func TestUploadBodyStagesProportionallyToExpectedSize(t *testing.T) {
	tests := []struct {
		name         string
		expected     int64
		source       io.Reader
		wantBuffers  int
		wantCapacity int
	}{
		{name: "empty", expected: 0, source: bytes.NewReader(nil)},
		{
			name: "one KiB", expected: 1 << 10, source: bytes.NewReader(make([]byte, 1<<10)),
			wantBuffers: 1, wantCapacity: 1 << 10,
		},
		{
			name: "just over one buffer", expected: uploadBodyBufferSize + 1,
			source:      bytes.NewReader(make([]byte, uploadBodyBufferSize+1)),
			wantBuffers: 2, wantCapacity: 2 * uploadBodyBufferSize,
		},
		{
			name: "maximum declared size", expected: math.MaxInt64, source: bytes.NewReader(nil),
			wantBuffers:  uploadBodyBufferCount,
			wantCapacity: uploadBodyBufferCount * uploadBodyBufferSize,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := newUploadBody(newExactSizeReader(tt.source, tt.expected, false))
			_, _ = body.Read(make([]byte, 1))
			gotBuffers, gotCapacity := stagedUploadCapacity(body)
			require.NoError(t, body.Close())
			body.waitReleased()

			assert.Equal(t, tt.wantBuffers, gotBuffers)
			assert.Equal(t, tt.wantCapacity, gotCapacity)
		})
	}
}

// concurrentUploadBodyResult captures one active small body's staging bound.
type concurrentUploadBodyResult struct {
	// buffers is the active staging-buffer count.
	buffers int
	// capacity is the active staging capacity in bytes.
	capacity int
	// err is any unexpected first-read or close failure.
	err error
}

// TestUploadBodyBoundsConcurrentSmallBodyStaging verifies many small request
// bodies retain capacity proportional to their payloads, not the large bound.
func TestUploadBodyBoundsConcurrentSmallBodyStaging(t *testing.T) {
	const (
		bodyCount = 32
		bodySize  = 1 << 10
	)
	results := make(chan concurrentUploadBodyResult, bodyCount)
	done := make(chan error, bodyCount)
	release := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(bodyCount)
	for range bodyCount {
		go func() {
			defer workers.Done()
			body := newUploadBody(newExactSizeReader(
				bytes.NewReader(make([]byte, bodySize)), bodySize, false))
			_, readErr := body.Read(make([]byte, 1))
			buffers, capacity := stagedUploadCapacity(body)
			results <- concurrentUploadBodyResult{
				buffers: buffers, capacity: capacity, err: readErr,
			}
			<-release
			closeErr := body.Close()
			body.waitReleased()
			done <- closeErr
		}()
	}

	totalCapacity := 0
	var resultErrors []error
	for range bodyCount {
		result := <-results
		totalCapacity += result.capacity
		if result.err != nil {
			resultErrors = append(resultErrors, result.err)
		}
		assert.Equal(t, 1, result.buffers)
		assert.Equal(t, bodySize, result.capacity)
	}
	close(release)
	workers.Wait()
	for range bodyCount {
		if err := <-done; err != nil {
			resultErrors = append(resultErrors, err)
		}
	}

	assert.Empty(t, resultErrors)
	assert.Equal(t, bodyCount*bodySize, totalCapacity)
}
