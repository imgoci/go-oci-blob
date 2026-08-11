package blob

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordedProgress collects (done, total) callback pairs.
type recordedProgress struct {
	dones  []int64
	totals []int64
}

func (r *recordedProgress) fn(done, total int64) {
	r.dones = append(r.dones, done)
	r.totals = append(r.totals, total)
}

func TestProgressTracker(t *testing.T) {
	t.Run("a nil tracker absorbs every call", func(t *testing.T) {
		var tracker *progressTracker

		assert.NotPanics(t, func() {
			tracker.add(10)
			tracker.set(20)
			tracker.setTotal(30)
		})
	})

	t.Run("nil callbacks build a nil tracker", func(t *testing.T) {
		assert.Nil(t, newProgressTracker(nil, 100))
	})

	t.Run("add accumulates and reports the total", func(t *testing.T) {
		rec := &recordedProgress{}
		tracker := newProgressTracker(rec.fn, 30)

		tracker.add(10)
		tracker.add(5)
		tracker.add(0)
		tracker.add(15)

		assert.Equal(t, []int64{10, 15, 30}, rec.dones, "zero adds report nothing")
		assert.Equal(t, []int64{30, 30, 30}, rec.totals)
	})

	t.Run("set suppresses positions at or below the committed count", func(t *testing.T) {
		rec := &recordedProgress{}
		tracker := newProgressTracker(rec.fn, 30)

		tracker.set(10)
		tracker.set(20)
		tracker.set(5)  // a restarted attempt replays earlier positions
		tracker.set(20) // reaching the old high-water mark stays silent
		tracker.set(25)

		assert.Equal(t, []int64{10, 20, 25}, rec.dones,
			"a restart must stay silent until it passes its predecessor")
	})

	t.Run("setTotal updates what later callbacks report", func(t *testing.T) {
		rec := &recordedProgress{}
		tracker := newProgressTracker(rec.fn, -1)

		tracker.add(5)
		tracker.setTotal(50)
		tracker.add(5)

		assert.Equal(t, []int64{-1, 50}, rec.totals)
	})

	t.Run("aggregates concurrent adds without losing bytes", func(t *testing.T) {
		var last int64
		var monotonic = true
		var prev int64
		tracker := newProgressTracker(func(done, _ int64) {
			if done < prev {
				monotonic = false
			}
			prev = done
			last = done
		}, 400)

		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() {
				for range 100 {
					tracker.add(1)
				}
			})
		}
		wg.Wait()

		require.True(t, monotonic, "aggregated count must never move backward")
		assert.Equal(t, int64(400), last, "every added byte must be counted exactly once")
	})
}
