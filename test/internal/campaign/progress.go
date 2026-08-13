package campaign

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// progressProbe validates callback order and overlap within one transfer.
type progressProbe struct {
	// mu protects counts and totals.
	mu sync.Mutex
	// counts contains every reported completed-byte value.
	counts []int64
	// totals contains every reported total.
	totals []int64
	// active detects callback overlap.
	active atomic.Int64
	// overlapped records whether callbacks overlapped.
	overlapped atomic.Bool
}

// callback records one synchronous progress report and briefly widens overlap
// detection without materially slowing the campaign.
func (probe *progressProbe) callback(done, total int64) {
	if probe.active.Add(1) != 1 {
		probe.overlapped.Store(true)
	}
	defer probe.active.Add(-1)
	time.Sleep(200 * time.Microsecond)
	probe.mu.Lock()
	defer probe.mu.Unlock()
	probe.counts = append(probe.counts, done)
	probe.totals = append(probe.totals, total)
}

// validate checks monotonic counts, documented known-or-unknown totals, exact
// completion, and per-transfer callback serialization.
func (probe *progressProbe) validate(want int64) error {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	if len(probe.counts) == 0 {
		return errors.New("progress callback was never called")
	}
	if probe.overlapped.Load() {
		return errors.New("progress callbacks overlapped within one transfer")
	}
	previous := int64(-1)
	for index, done := range probe.counts {
		if done < previous {
			return fmt.Errorf("progress moved backward at callback %d", index)
		}
		if probe.totals[index] != want && probe.totals[index] != -1 {
			return fmt.Errorf("progress total was %d, want %d or -1", probe.totals[index], want)
		}
		previous = done
	}
	if previous != want {
		return fmt.Errorf("final progress was %d, want %d", previous, want)
	}
	return nil
}
