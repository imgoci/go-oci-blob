package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
)

// parallel.go implements the WithParallelPull path: workers fetch
// ranged chunks concurrently while the reader emits them strictly in
// order, so the digest-verifying reader above works unchanged. The
// first chunk's ranged request doubles as the probe: a 206 proves
// range support and carries the total size, while a 200 already IS
// the single-stream fallback, so no request is wasted either way.

// chunkResult carries one fetched chunk (or its failure) to the
// in-order reader.
type chunkResult struct {
	// buf is the pooled backing buffer to recycle after draining.
	buf *[]byte
	// data is the chunk's bytes within buf.
	data []byte
	// err reports a chunk that could not be fetched.
	err error
}

// parallelPuller emits concurrently-fetched chunks in order. It
// implements [io.ReadCloser] under the verifying reader.
type parallelPuller struct {
	// cancel stops the dispatcher and every in-flight fetch.
	cancel context.CancelFunc
	// results delivers one future per chunk, in chunk order; its
	// buffer caps how many chunks can be in flight at once.
	results chan chan chunkResult
	// pool recycles chunk buffers.
	pool *sync.Pool
	// current is the chunk being drained, backed by currentBuf.
	current []byte
	// currentBuf is returned to the pool once current is drained.
	currentBuf *[]byte
	// pos is the read position within current.
	pos int
	// err is sticky: once a read fails or ends, it stays failed.
	err error
}

// parallelPull starts a parallel download of the blob at target and
// returns the in-order reader. It issues the probe request for the
// first chunk synchronously and falls back to a plain stream when
// the registry does not serve ranges (200 probe answer, or 416 for
// an empty blob).
func (c *Client) parallelPull(
	ctx context.Context, target *url.URL, tracker *progressTracker,
) (io.ReadCloser, error) {
	probe, err := c.get(ctx, target, fmt.Sprintf("bytes=0-%d", c.pullChunk-1)) //nolint:bodyclose // reader owns body
	if err != nil {
		var regErr *registryError
		if errors.As(err, &regErr) && regErr.status == http.StatusRequestedRangeNotSatisfiable {
			// An empty blob satisfies no range; fetch it plainly.
			return c.singleStream(ctx, target, tracker)
		}
		return nil, err
	}

	if probe.StatusCode != http.StatusPartialContent {
		// The registry ignored the range: this response already is
		// the whole blob, so use it as the single-stream fallback.
		tracker.setTotal(probe.ContentLength)
		stream := &resumeReader{ctx: ctx, client: c, target: target, body: probe.Body}
		return progressify(stream, tracker), nil
	}
	total, ok := parseContentRangeTotal(probe.Header.Get("Content-Range"))
	if !ok {
		// Range support without a usable total; a plain stream is
		// the only safe plan.
		drainAndClose(probe.Body)
		return c.singleStream(ctx, target, tracker)
	}

	tracker.setTotal(total)
	pullCtx, cancel := context.WithCancel(ctx)
	puller := &parallelPuller{
		cancel:  cancel,
		results: make(chan chan chunkResult, c.pullWorkers),
		pool:    c.bufPool,
	}
	go puller.dispatch(pullCtx, c, target, total, probe.Body, tracker)
	return puller, nil
}

// singleStream opens a plain full-blob GET wrapped in the usual
// resume layer, reporting delivered bytes to the tracker.
func (c *Client) singleStream(
	ctx context.Context, target *url.URL, tracker *progressTracker,
) (io.ReadCloser, error) {
	resp, err := c.get(ctx, target, "") //nolint:bodyclose // the returned reader owns the body
	if err != nil {
		return nil, err
	}
	tracker.setTotal(resp.ContentLength)
	stream := &resumeReader{ctx: ctx, client: c, target: target, body: resp.Body}
	return progressify(stream, tracker), nil
}

// dispatch schedules every chunk fetch, keeping at most one worker
// per concurrency slot and enqueueing futures in chunk order.
// probeBody is the already-open response for chunk zero.
func (p *parallelPuller) dispatch(
	ctx context.Context,
	c *Client,
	target *url.URL,
	total int64,
	probeBody io.ReadCloser,
	tracker *progressTracker,
) {
	defer close(p.results)

	chunk := c.pullChunk
	numChunks := (total + chunk - 1) / chunk

	// Chunk zero is already in flight as the probe; read its body in
	// a worker without taking a concurrency slot.
	first := make(chan chunkResult, 1)
	go func() {
		want := min(chunk, total)
		buf, data, err := readChunkInto(p.pool, probeBody, want)
		_ = probeBody.Close()
		if err != nil {
			// The probe body broke; refetch chunk zero whole.
			first <- c.fetchChunk(ctx, target, 0, want, p.pool, tracker)
			return
		}
		tracker.add(want)
		first <- chunkResult{buf: buf, data: data}
	}()
	select {
	case p.results <- first:
	case <-ctx.Done():
		return
	}

	slots := make(chan struct{}, c.pullWorkers)
	for i := int64(1); i < numChunks; i++ {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		start := i * chunk
		want := min(chunk, total-start)
		future := make(chan chunkResult, 1)
		go func() {
			defer func() { <-slots }()
			future <- c.fetchChunk(ctx, target, start, want, p.pool, tracker)
		}()
		select {
		case p.results <- future:
		case <-ctx.Done():
			return
		}
	}
}

// fetchChunk downloads the want bytes at start with a ranged GET,
// retrying broken chunk bodies under the client's policy. A fetched
// chunk counts toward aggregated progress exactly once.
func (c *Client) fetchChunk(
	ctx context.Context, target *url.URL, start, want int64, pool *sync.Pool, tracker *progressTracker,
) chunkResult {
	rangeHeader := fmt.Sprintf("bytes=%d-%d", start, start+want-1)
	attempts := c.retry.attempts()
	for attempt := 1; ; attempt++ {
		result := c.fetchChunkOnce(ctx, target, rangeHeader, want, pool)
		if result.err == nil {
			tracker.add(want)
			return result
		}
		if ctx.Err() != nil || attempt == attempts {
			return result
		}
		if err := sleepContext(ctx, c.retry.backoffDelay(attempt, 0)); err != nil {
			return result
		}
	}
}

// fetchChunkOnce performs one ranged GET for a chunk and reads its
// body fully into a pooled buffer.
func (c *Client) fetchChunkOnce(
	ctx context.Context, target *url.URL, rangeHeader string, want int64, pool *sync.Pool,
) chunkResult {
	resp, err := c.get(ctx, target, rangeHeader)
	if err != nil {
		return chunkResult{err: fmt.Errorf("fetching chunk %s: %w", rangeHeader, err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return chunkResult{err: fmt.Errorf(
			"fetching chunk %s: registry stopped honoring range requests (status %d)",
			rangeHeader, resp.StatusCode)}
	}
	buf, data, err := readChunkInto(pool, resp.Body, want)
	if err != nil {
		return chunkResult{err: fmt.Errorf("reading chunk %s: %w", rangeHeader, err)}
	}
	return chunkResult{buf: buf, data: data}
}

// readChunkInto reads exactly want bytes from r into a pooled buffer.
// On failure the buffer goes straight back to the pool.
func readChunkInto(pool *sync.Pool, r io.Reader, want int64) (*[]byte, []byte, error) {
	buf, ok := pool.Get().(*[]byte)
	if !ok || int64(len(*buf)) < want {
		fresh := make([]byte, want)
		buf = &fresh
	}
	data := (*buf)[:want]
	if _, err := io.ReadFull(r, data); err != nil {
		pool.Put(buf)
		return nil, nil, err
	}
	return buf, data, nil
}

// Read copies from the chunk being drained, blocking on the next
// in-order chunk when the current one is exhausted.
func (p *parallelPuller) Read(b []byte) (int, error) {
	if p.err != nil {
		return 0, p.err
	}
	for p.pos == len(p.current) {
		p.releaseCurrent()
		future, ok := <-p.results
		if !ok {
			p.err = io.EOF
			return 0, io.EOF
		}
		result := <-future
		if result.err != nil {
			p.err = result.err
			return 0, p.err
		}
		p.current, p.currentBuf, p.pos = result.data, result.buf, 0
	}
	n := copy(b, p.current[p.pos:])
	p.pos += n
	return n, nil
}

// Close stops the download and frees every in-flight chunk buffer.
func (p *parallelPuller) Close() error {
	p.cancel()
	p.releaseCurrent()
	if p.err == nil {
		p.err = errors.New("blob: parallel pull closed")
	}
	// Drain enqueued futures so their buffers return to the pool;
	// every enqueued future has a running worker that will send.
	for future := range p.results {
		if result := <-future; result.buf != nil {
			p.pool.Put(result.buf)
		}
	}
	return nil
}

// releaseCurrent recycles the drained chunk buffer, if any.
func (p *parallelPuller) releaseCurrent() {
	if p.currentBuf != nil {
		p.pool.Put(p.currentBuf)
		p.currentBuf, p.current, p.pos = nil, nil, 0
	}
}
