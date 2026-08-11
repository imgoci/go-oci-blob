package blob

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParallelBufferReserveBoundsConcurrentRetention proves simultaneous
// returns cannot retain more payload buffers than the configured worker count.
func TestParallelBufferReserveBoundsConcurrentRetention(t *testing.T) {
	const workers = 4
	client := New(WithParallelPull(workers, 1<<20))
	pool := newChunkBufferPool(client.bufPool)

	var returned sync.WaitGroup
	for range workers * 8 {
		returned.Go(func() {
			pool.put(make([]byte, 1, 1<<20))
		})
	}
	returned.Wait()

	require.Len(t, client.bufPool, workers,
		"the client must not retain more than one payload buffer per worker")
	for range workers {
		assert.Equal(t, 1<<20, cap(pool.take()))
	}
	assert.Nil(t, pool.take(), "taking every retained buffer must empty the reserve")
}
