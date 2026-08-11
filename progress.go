package blob

import (
	"io"
	"sync"
)

// progressTracker turns raw byte counts from anywhere in a transfer
// into monotonic cumulative callbacks. A nil tracker is valid and
// reports nothing, so transfer paths call it unconditionally.
type progressTracker struct {
	// mu serializes callbacks: chunk completions can land on several
	// goroutines during a parallel pull.
	mu sync.Mutex
	// fn is the caller's WithProgress callback.
	fn func(done, total int64)
	// total is the blob or window size, or -1 when unknown.
	total int64
	// done is the accepted or delivered cumulative count reported so far.
	done int64
}

// newProgressTracker builds a tracker for fn, or nil when fn is nil.
func newProgressTracker(fn func(done, total int64), total int64) *progressTracker {
	if fn == nil {
		return nil
	}
	return &progressTracker{fn: fn, total: total}
}

// setTotal records a total learned after the tracker was built, such
// as a Content-Length that only arrives with the response.
func (t *progressTracker) setTotal(total int64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.total = total
}

// add reports n freshly moved bytes on top of the cumulative count.
func (t *progressTracker) add(n int64) {
	if t == nil || n <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.done += n
	t.fn(t.done, t.total)
}

// set reports that the transfer has reached the absolute position
// pos. Positions at or below the prior count are suppressed, so
// a restarted attempt stays silent until it passes its predecessor.
func (t *progressTracker) set(pos int64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if pos <= t.done {
		return
	}
	t.done = pos
	t.fn(t.done, t.total)
}

// progressReader counts bytes delivered downstream into a tracker.
type progressReader struct {
	// body is the wrapped stream.
	body io.ReadCloser
	// tracker receives the delivered-byte counts; may be nil.
	tracker *progressTracker
}

// progressify wraps body so delivered bytes feed the tracker. A nil
// tracker returns body unchanged.
func progressify(body io.ReadCloser, tracker *progressTracker) io.ReadCloser {
	if tracker == nil {
		return body
	}
	return &progressReader{body: body, tracker: tracker}
}

// Read delivers bytes and counts them.
func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.body.Read(b)
	p.tracker.add(int64(n))
	return n, err
}

// Close closes the wrapped stream.
func (p *progressReader) Close() error {
	return p.body.Close()
}
