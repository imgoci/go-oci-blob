package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// parallel.go implements the WithParallelPull path: workers fetch
// ranged chunks concurrently while the reader emits them strictly in
// order, so the digest-verifying reader above works unchanged. The
// first chunk's ranged request doubles as the probe: a 206 proves
// range support and carries the total size, while a 200 already IS
// the single-stream fallback, so no request is wasted either way.

// errParallelPullClosed is returned by reads after the pull is closed.
var errParallelPullClosed = errors.New("blob: parallel pull closed")

// parallelReadBufferSize bounds each incremental body read.
const parallelReadBufferSize = 32 << 10

// chunkResult carries one fetched chunk (or its failure) to the
// in-order reader.
type chunkResult struct {
	// buf is the pooled backing buffer to recycle after draining.
	buf *[]byte
	// data is the chunk's bytes within buf.
	data []byte
	// err reports a chunk that could not be fetched.
	err error
	// slot records that this result owns one concurrency-and-buffer
	// token until the reader consumes or discards it.
	slot bool
}

// trackedBody lets Close interrupt response-body reads even when a
// custom RoundTripper does not react to request-context cancellation.
type trackedBody struct {
	// body is the response stream being tracked.
	body io.ReadCloser
	// once makes concurrent worker and puller closes idempotent.
	once sync.Once
	// err is the result of the first close.
	err error
}

// Close closes the underlying response body once.
func (b *trackedBody) Close() error {
	b.once.Do(func() { b.err = b.body.Close() })
	return b.err
}

// Read forwards reads to the underlying response body.
func (b *trackedBody) Read(p []byte) (int, error) {
	return b.body.Read(p)
}

// parallelPuller emits concurrently-fetched chunks in order. It
// implements [io.ReadCloser] under the verifying reader.
type parallelPuller struct {
	// ctx is canceled by Close and bounds every worker request.
	ctx context.Context
	// cancel stops the dispatcher and every in-flight fetch.
	cancel context.CancelFunc
	// results delivers one future per chunk, in chunk order.
	results chan chan chunkResult
	// slots bounds response bodies plus completed buffers together.
	slots chan struct{}
	// pool recycles chunk buffers.
	pool *sync.Pool

	// mu protects reader state, closure, and active response bodies.
	mu sync.Mutex
	// active contains bodies Close can interrupt.
	active map[*trackedBody]struct{}
	// current is the chunk being drained, backed by currentBuf.
	current []byte
	// currentBuf is returned to the pool once current is drained.
	currentBuf *[]byte
	// currentSlot records that current owns one slot.
	currentSlot bool
	// pos is the read position within current.
	pos int
	// err is sticky: once a read fails or ends, it stays failed.
	err error
	// closed prevents results arriving after Close from being adopted.
	closed bool
	// closeOnce makes Close idempotent.
	closeOnce sync.Once
}

// parallelPull starts a parallel download of the blob at target and
// returns the in-order reader. It issues the probe request for the first chunk
// synchronously, uses a 200 probe body as the plain stream, and recognizes a
// 416 with "bytes */0" as a complete empty blob. Other 416 responses can use a
// plain GET only when the lifetime request budget has attempts left.
func (c *Client) parallelPull(
	ctx context.Context, target *url.URL, tracker *progressTracker,
) (io.ReadCloser, error) {
	pullCtx, cancel := context.WithCancel(ctx)
	probeRange := fmt.Sprintf("bytes=0-%d", c.pullChunk-1)
	probe, probeAttempts, err := c.parallelProbe(pullCtx, target, probeRange)
	if err != nil {
		cancel()
		return nil, err
	}

	if probe.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		empty := unsatisfiedRangeTotal(probe.Header.Get("Content-Range")) == 0
		drainAndClose(probe.Body)
		cancel()
		if empty {
			tracker.setTotal(0)
			body := &requestAttemptBody{ReadCloser: http.NoBody, attempts: probeAttempts}
			stream := newResumeReader(ctx, c, target, body)
			return progressify(stream, tracker), nil
		}
		return c.singleStreamAfterAttempts(ctx, target, tracker, probeAttempts)
	}
	if probe.StatusCode != http.StatusOK && probe.StatusCode != http.StatusPartialContent {
		defer probe.Body.Close()
		cancel()
		return nil, interpretError(probe)
	}
	if probe.StatusCode != http.StatusPartialContent {
		// The registry ignored the range: this response already is
		// the whole blob, so use it as the single-stream fallback.
		tracker.setTotal(probe.ContentLength)
		body := &requestAttemptBody{
			ReadCloser: &cancelBody{ReadCloser: probe.Body, cancel: cancel},
			attempts:   probeAttempts,
		}
		stream := newResumeReader(ctx, c, target, body)
		return progressify(stream, tracker), nil
	}

	parsed, ok := parseContentRange(probe.Header.Get("Content-Range"))
	if !ok || parsed.start != 0 || parsed.end >= c.pullChunk {
		drainAndClose(probe.Body)
		cancel()
		return nil, fmt.Errorf("parallel pull probe %s: registry returned invalid Content-Range %q",
			probeRange, probe.Header.Get("Content-Range"))
	}

	tracker.setTotal(parsed.total)
	puller := &parallelPuller{
		ctx:     pullCtx,
		cancel:  cancel,
		results: make(chan chan chunkResult, c.pullWorkers),
		slots:   make(chan struct{}, c.pullWorkers),
		pool:    c.bufPool,
		active:  make(map[*trackedBody]struct{}),
	}
	// The already-open probe consumes one worker/buffer slot.
	puller.slots <- struct{}{}
	probeBody, tracked := puller.trackBody(probe.Body)
	if !tracked {
		cancel()
		return nil, errParallelPullClosed
	}
	go puller.dispatch(c, target, parsed, probeBody, probeAttempts)
	return progressify(puller, tracker), nil
}

// cancelBody couples the probe request's context to fallback-body
// ownership, so closing or replacing that first body releases it.
type cancelBody struct {
	// ReadCloser is the probe response body.
	io.ReadCloser

	cancel context.CancelFunc
	once   sync.Once
}

// Close closes the response and cancels the request context once.
func (b *cancelBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.cancel)
	return err
}

// singleStreamAfterAttempts opens a full-blob GET using only the request
// budget remaining after spent earlier attempts.
func (c *Client) singleStreamAfterAttempts(
	ctx context.Context, target *url.URL, tracker *progressTracker, spent int,
) (io.ReadCloser, error) {
	resp, err := c.getAfterAttempts(ctx, target, "", spent) //nolint:bodyclose // the returned reader owns the body
	if err != nil {
		return nil, err
	}
	tracker.setTotal(resp.ContentLength)
	stream := newResumeReader(ctx, c, target, resp.Body)
	return progressify(stream, tracker), nil
}

// unsatisfiedRangeTotal parses "bytes */<total>" from a 416 response. A
// malformed or non-byte value returns -1.
func unsatisfiedRangeTotal(header string) int64 {
	unit, rest, found := strings.Cut(strings.TrimSpace(header), " ")
	if !found || !strings.EqualFold(unit, "bytes") {
		return -1
	}
	interval, totalText, found := strings.Cut(rest, "/")
	if !found || interval != "*" {
		return -1
	}
	total, err := parseDecimal(totalText)
	if err != nil {
		return -1
	}
	return total
}

// parallelProbe performs the initial ranged GET without hiding how
// much of the retry budget it consumed. Body-read retries can then
// share the same total MaxAttempts budget.
func (c *Client) parallelProbe(
	ctx context.Context, target *url.URL, rangeHeader string,
) (*http.Response, int, error) {
	attempts := c.retry.attempts()
	for attempt := 1; ; attempt++ {
		resp, err := c.rangeRequest(ctx, target, rangeHeader)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, attempt, ctxErr
			}
			if attempt == attempts {
				return nil, attempt, err
			}
			if err := sleepContext(ctx, c.retry.backoffDelay(attempt, 0)); err != nil {
				return nil, attempt, err
			}
			continue
		}
		if !retryableStatus(resp.StatusCode) || attempt == attempts {
			return resp, attempt, nil
		}
		retryAfter := retryAfterDelay(resp.Header.Get("Retry-After"), time.Now())
		drainAndClose(resp.Body)
		if err := sleepContext(ctx, c.retry.backoffDelay(attempt, retryAfter)); err != nil {
			return nil, attempt, err
		}
	}
}

// rangeRequest performs exactly one ranged GET. Retry ownership stays
// with the parallel fetcher, avoiding nested retry multiplication.
func (c *Client) rangeRequest(
	ctx context.Context, target *url.URL, rangeHeader string,
) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("building ranged request: %w", err)
	}
	req.Header.Set("Range", rangeHeader)
	resp, err := c.doRegistryRequest(req)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	return resp, nil
}

// dispatch schedules every chunk fetch, keeping response bodies plus
// completed buffers within the configured worker count. probeBody is
// the already-open response for the first returned interval.
func (p *parallelPuller) dispatch(
	c *Client,
	target *url.URL,
	probeRange contentRange,
	probeBody *trackedBody,
	probeAttempts int,
) {
	defer close(p.results)
	if !p.scheduleProbe(c, target, probeRange, probeBody, probeAttempts) {
		return
	}

	chunk := c.pullChunk
	for start := probeRange.end + 1; start < probeRange.total; {
		want := min(chunk, probeRange.total-start)
		if !p.scheduleChunk(c, target, start, want, probeRange.total) {
			return
		}
		start += want
	}
}

// scheduleProbe enqueues and starts the already-open first range body.
func (p *parallelPuller) scheduleProbe(
	c *Client,
	target *url.URL,
	probeRange contentRange,
	probeBody *trackedBody,
	probeAttempts int,
) bool {
	if p.ctx.Err() != nil {
		p.untrackAndClose(probeBody)
		p.releaseResult(chunkResult{slot: true})
		return false
	}
	first := make(chan chunkResult, 1)
	select {
	case p.results <- first:
	case <-p.ctx.Done():
		p.untrackAndClose(probeBody)
		p.releaseResult(chunkResult{slot: true})
		return false
	}
	go func() {
		want := probeRange.end - probeRange.start + 1
		buf, data, err := readChunkInto(p.pool, probeBody, want)
		p.untrackAndClose(probeBody)
		if err == nil {
			first <- chunkResult{buf: buf, data: data, slot: true}
			return
		}
		if ctxErr := p.ctx.Err(); ctxErr != nil {
			first <- chunkResult{err: ctxErr, slot: true}
			return
		}
		remaining := c.retry.attempts() - probeAttempts
		if remaining <= 0 {
			first <- chunkResult{err: fmt.Errorf("reading probe range: %w", err), slot: true}
			return
		}
		first <- c.fetchChunk(p.ctx, target, 0, want, probeRange.total, p, remaining)
	}()
	return true
}

// scheduleChunk reserves one slot, enqueues its future in order, and starts
// the ranged fetch unless cancellation wins first.
func (p *parallelPuller) scheduleChunk(
	c *Client, target *url.URL, start, want, total int64,
) bool {
	select {
	case p.slots <- struct{}{}:
	case <-p.ctx.Done():
		return false
	}
	if p.ctx.Err() != nil {
		p.releaseResult(chunkResult{slot: true})
		return false
	}
	future := make(chan chunkResult, 1)
	select {
	case p.results <- future:
	case <-p.ctx.Done():
		p.releaseResult(chunkResult{slot: true})
		return false
	}
	if ctxErr := p.ctx.Err(); ctxErr != nil {
		future <- chunkResult{err: ctxErr, slot: true}
		return false
	}
	go func() {
		future <- c.fetchChunk(p.ctx, target, start, want, total, p, c.retry.attempts())
	}()
	return true
}

// fetchChunk downloads exactly want bytes at start. A compliant
// server may return a shorter 206 interval, in which case the worker
// continues at the reported end without leaving a gap.
func (c *Client) fetchChunk(
	ctx context.Context,
	target *url.URL,
	start, want, total int64,
	p *parallelPuller,
	attempts int,
) chunkResult {
	buf := takeBuffer(p.pool)
	data := (*buf)[:0]
	end := start + want - 1
	for cursor := start; cursor <= end; {
		var next int64
		var err error
		data, next, err = c.fetchRangePart(ctx, target, cursor, end, total, data, p, attempts)
		if err != nil {
			*buf = data[:0]
			p.pool.Put(buf)
			return chunkResult{err: err, slot: true}
		}
		cursor = next
	}
	*buf = data
	return chunkResult{buf: buf, data: data, slot: true}
}

// fetchRangePart fetches the interval beginning at start. It accepts
// a shorter valid 206 and returns the next byte that still needs to be
// fetched. attempts is the total budget for transport/status/body
// failures of this request.
func (c *Client) fetchRangePart(
	ctx context.Context,
	target *url.URL,
	start, end, total int64,
	data []byte,
	p *parallelPuller,
	attempts int,
) ([]byte, int64, error) {
	rangeHeader := fmt.Sprintf("bytes=%d-%d", start, end)
	for attempt := 1; ; attempt++ {
		result := c.fetchRangePartOnce(ctx, target, rangeHeader, start, end, total, data, p)
		data = result.data
		if result.err == nil {
			return data, result.next, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return data, start, ctxErr
		}
		if !result.retryable || attempt == attempts {
			return data, start, result.err
		}
		waitErr := sleepContext(ctx, c.retry.backoffDelay(attempt, result.retryAfter))
		if waitErr != nil {
			return data, start, waitErr
		}
	}
}

// rangePartResult is one ranged-request outcome.
type rangePartResult struct {
	// data contains bytes retained from prior successful portions.
	data []byte
	// next is the next required offset after success.
	next int64
	// err describes a failed attempt.
	err error
	// retryable allows another attempt under the policy.
	retryable bool
	// retryAfter is the registry-requested delay.
	retryAfter time.Duration
}

// fetchRangePartOnce makes and validates one request for rangeHeader.
func (c *Client) fetchRangePartOnce(
	ctx context.Context,
	target *url.URL,
	rangeHeader string,
	start, end, total int64,
	data []byte,
	p *parallelPuller,
) rangePartResult {
	resp, err := c.rangeRequest(ctx, target, rangeHeader)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
		}
		return rangePartResult{
			data: data, err: fmt.Errorf("fetching chunk %s: %w", rangeHeader, err), retryable: ctx.Err() == nil,
		}
	}
	if resp.StatusCode != http.StatusPartialContent {
		return unexpectedRangePart(resp, rangeHeader, data)
	}

	parsed, ok := parseContentRange(resp.Header.Get("Content-Range"))
	if !ok || parsed.start != start || parsed.end > end || parsed.total != total {
		drainAndClose(resp.Body)
		return rangePartResult{data: data, err: fmt.Errorf(
			"fetching chunk %s: registry returned invalid Content-Range %q",
			rangeHeader, resp.Header.Get("Content-Range"))}
	}
	body, tracked := p.trackBody(resp.Body)
	if !tracked {
		return rangePartResult{data: data, err: errParallelPullClosed}
	}
	before := len(data)
	partLength := parsed.end - parsed.start + 1
	data, err = appendExactly(data, body, partLength)
	p.untrackAndClose(body)
	if err != nil {
		return rangePartResult{
			data: data[:before], err: fmt.Errorf("reading chunk %s: %w", rangeHeader, err), retryable: true,
		}
	}
	return rangePartResult{data: data, next: parsed.end + 1}
}

// unexpectedRangePart interprets a non-206 response to a chunk GET.
func unexpectedRangePart(resp *http.Response, rangeHeader string, data []byte) rangePartResult {
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		drainAndClose(resp.Body)
		return rangePartResult{data: data, err: fmt.Errorf(
			"fetching chunk %s: registry stopped honoring range requests (status %d)",
			rangeHeader, resp.StatusCode)}
	}
	retryable := retryableStatus(resp.StatusCode)
	retryAfter := retryAfterDelay(resp.Header.Get("Retry-After"), time.Now())
	regErr := interpretError(resp)
	_ = resp.Body.Close()
	return rangePartResult{
		data: data, err: fmt.Errorf("fetching chunk %s: %w", rangeHeader, regErr),
		retryable: retryable, retryAfter: retryAfter,
	}
}

// readChunkInto reads exactly want bytes from r into a pooled buffer.
// It grows according to bytes actually read rather than trusting a
// potentially enormous configured chunk size up front.
func readChunkInto(pool *sync.Pool, r io.Reader, want int64) (*[]byte, []byte, error) {
	buf := takeBuffer(pool)
	data, err := appendExactly((*buf)[:0], r, want)
	if err != nil {
		*buf = data[:0]
		pool.Put(buf)
		return nil, nil, err
	}
	*buf = data
	return buf, data, nil
}

// takeBuffer returns an empty pooled buffer, allocating only the
// slice header when the pool has not retained one.
func takeBuffer(pool *sync.Pool) *[]byte {
	if buf, ok := pool.Get().(*[]byte); ok {
		*buf = (*buf)[:0]
		return buf
	}
	buf := make([]byte, 0)
	return &buf
}

// appendExactly appends want bytes without converting an unchecked
// int64 into a slice length or allocating the declared range at once.
func appendExactly(dst []byte, r io.Reader, want int64) ([]byte, error) {
	if want < 0 || uint64(want) > uint64(^uint(0)>>1)-uint64(len(dst)) {
		return dst, fmt.Errorf("range length %d exceeds addressable memory", want)
	}
	scratch := make([]byte, min(want, parallelReadBufferSize))
	for remaining := want; remaining > 0; {
		step := min(remaining, int64(len(scratch)))
		n, err := io.ReadFull(r, scratch[:int(step)])
		dst = append(dst, scratch[:n]...)
		remaining -= int64(n)
		if err != nil {
			return dst, err
		}
	}
	return dst, nil
}

// trackBody registers body for interruption by Close.
func (p *parallelPuller) trackBody(body io.ReadCloser) (*trackedBody, bool) {
	tracked := &trackedBody{body: body}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		_ = tracked.Close()
		return nil, false
	}
	p.active[tracked] = struct{}{}
	p.mu.Unlock()
	return tracked, true
}

// untrackAndClose closes body and removes it from the active set.
func (p *parallelPuller) untrackAndClose(body *trackedBody) {
	_ = body.Close()
	p.mu.Lock()
	delete(p.active, body)
	p.mu.Unlock()
}

// Read copies from the chunk being drained, blocking on the next
// in-order chunk when the current one is exhausted.
func (p *parallelPuller) Read(b []byte) (int, error) {
	for {
		if n, ready, err := p.readCurrent(b); ready {
			return n, err
		}
		result, err := p.nextResult()
		if err != nil {
			return 0, p.terminalError(err)
		}
		if result.err != nil {
			p.releaseResult(result)
			return 0, p.terminalError(result.err)
		}
		if err := p.adoptResult(result); err != nil {
			p.releaseResult(result)
			return 0, p.terminalError(err)
		}
	}
}

// readCurrent serves buffered data or reports a terminal state. A
// false ready result asks Read to await the next chunk.
func (p *parallelPuller) readCurrent(b []byte) (int, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return 0, true, p.err
	}
	if ctxErr := p.ctx.Err(); ctxErr != nil {
		p.err = ctxErr
		return 0, true, ctxErr
	}
	if len(b) == 0 {
		return 0, true, nil
	}
	if p.pos < len(p.current) {
		n := copy(b, p.current[p.pos:])
		p.pos += n
		return n, true, nil
	}
	p.releaseCurrentLocked()
	return 0, false, nil
}

// nextResult waits for the next ordered result while allowing
// cancellation to win over an already-ready future.
func (p *parallelPuller) nextResult() (chunkResult, error) {
	var future chan chunkResult
	select {
	case next, ok := <-p.results:
		if !ok {
			return chunkResult{}, io.EOF
		}
		future = next
	case <-p.ctx.Done():
		return chunkResult{}, p.ctx.Err()
	}
	if ctxErr := p.ctx.Err(); ctxErr != nil {
		go func() { p.releaseResult(<-future) }()
		return chunkResult{}, ctxErr
	}
	select {
	case result := <-future:
		if ctxErr := p.ctx.Err(); ctxErr != nil {
			p.releaseResult(result)
			return chunkResult{}, ctxErr
		}
		return result, nil
	case <-p.ctx.Done():
		go func() { p.releaseResult(<-future) }()
		return chunkResult{}, p.ctx.Err()
	}
}

// adoptResult makes result the buffer served by the next Read.
func (p *parallelPuller) adoptResult(result chunkResult) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errParallelPullClosed
	}
	if ctxErr := p.ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	p.current, p.currentBuf = result.data, result.buf
	p.currentSlot, p.pos = result.slot, 0
	return nil
}

// Close stops the download, interrupts active bodies, and schedules
// recycling of future buffers without waiting on an uncooperative
// custom transport.
func (p *parallelPuller) Close() error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		if p.err == nil {
			p.err = errParallelPullClosed
		}
		p.releaseCurrentLocked()
		bodies := make([]*trackedBody, 0, len(p.active))
		for body := range p.active {
			bodies = append(bodies, body)
		}
		p.mu.Unlock()
		p.cancel()
		for _, body := range bodies {
			_ = body.Close()
		}
		go p.drainResults()
	})
	return nil
}

// drainResults releases results not consumed by a racing Read.
func (p *parallelPuller) drainResults() {
	for future := range p.results {
		p.releaseResult(<-future)
	}
}

// terminalError records err unless another terminal state won the
// race, and returns the winning error.
func (p *parallelPuller) terminalError(err error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err == nil {
		p.err = err
	}
	return p.err
}

// releaseResult recycles a result's buffer and concurrency token.
func (p *parallelPuller) releaseResult(result chunkResult) {
	if result.buf != nil {
		*result.buf = (*result.buf)[:0]
		p.pool.Put(result.buf)
	}
	if result.slot {
		<-p.slots
	}
}

// releaseCurrentLocked recycles the current chunk. p.mu must be held.
func (p *parallelPuller) releaseCurrentLocked() {
	if p.currentBuf != nil {
		*p.currentBuf = (*p.currentBuf)[:0]
		p.pool.Put(p.currentBuf)
	}
	if p.currentSlot {
		<-p.slots
	}
	p.currentBuf, p.current, p.pos = nil, nil, 0
	p.currentSlot = false
}
