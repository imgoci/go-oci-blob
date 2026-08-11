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

// maxParallelRangeParts bounds successful partial responses per scheduled
// chunk. Normal registries satisfy a range in one response; sixteen still
// tolerates 64 KiB server-side fragments for a 1 MiB chunk without allowing
// unbounded request amplification.
const maxParallelRangeParts = 16

// chunkResult carries one fetched chunk (or its failure) to the
// in-order reader.
type chunkResult struct {
	// index is the chunk's position in the ordered result stream.
	index int64
	// data is the chunk's pooled backing buffer and readable bytes.
	data []byte
	// err reports a chunk that could not be fetched.
	err error
	// slot records that this result owns one concurrency-and-buffer
	// token until the reader consumes or discards it.
	slot bool
}

// chunkTask describes one complete ordered chunk to fetch.
type chunkTask struct {
	// index is the chunk's position in the ordered result stream.
	index int64
	// start is the first byte to fetch.
	start int64
	// want is the complete chunk length.
	want int64
	// total is the blob's complete length.
	total int64
	// probeBody is the already-open first response, when this is the probe task.
	probeBody *trackedBody
	// probeAttempts is the retry budget already spent opening probeBody.
	probeAttempts int
}

// chunkBufferPool bounds the client's reusable payload buffers explicitly,
// unlike [sync.Pool] entries whose retained count is runtime-controlled.
type chunkBufferPool struct {
	// buffers holds at most one reusable payload buffer per worker slot.
	buffers chan []byte
}

// newChunkBufferPool wraps the client's bounded concurrent buffer reserve.
func newChunkBufferPool(buffers chan []byte) *chunkBufferPool {
	return &chunkBufferPool{buffers: buffers}
}

// take returns one empty reusable buffer, or nil when every retained buffer is
// already in use.
func (p *chunkBufferPool) take() []byte {
	select {
	case buf := <-p.buffers:
		return buf[:0]
	default:
		return nil
	}
}

// put makes buf available to another task in the same pull.
func (p *chunkBufferPool) put(buf []byte) {
	if buf == nil {
		return
	}
	select {
	case p.buffers <- buf[:0]:
	default:
		// The worker-slot invariant normally makes this unreachable. Dropping
		// the buffer is safer than blocking cleanup if cancellation wins a race.
	}
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
	// results receives completed chunks from the fixed worker set.
	results chan chunkResult
	// slots bounds response bodies plus completed buffers together.
	slots chan struct{}
	// pool recycles chunk buffers through the client's bounded reserve.
	pool *chunkBufferPool

	// mu protects reader state, closure, and active response bodies.
	mu sync.Mutex
	// active contains bodies Close can interrupt.
	active map[*trackedBody]struct{}
	// pending holds out-of-order worker results by chunk index.
	pending map[int64]chunkResult
	// next is the next chunk index the reader must emit.
	next int64
	// current is the chunk being drained.
	current []byte
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
	workerCount := parallelWorkerCount(c.pullWorkers, c.pullChunk, parsed.end+1, parsed.total)
	puller := &parallelPuller{
		ctx:     pullCtx,
		cancel:  cancel,
		results: make(chan chunkResult, workerCount),
		slots:   make(chan struct{}, workerCount),
		pool:    newChunkBufferPool(c.bufPool),
		active:  make(map[*trackedBody]struct{}),
		pending: make(map[int64]chunkResult, workerCount),
	}
	// The already-open probe consumes one worker/buffer slot.
	puller.slots <- struct{}{}
	probeBody, tracked := puller.trackBody(probe.Body)
	if !tracked {
		cancel()
		return nil, errParallelPullClosed
	}
	go puller.dispatch(c, target, parsed, probeBody, probeAttempts, workerCount)
	return progressify(puller, tracker), nil
}

// parallelWorkerCount limits the fixed worker set to chunks this pull can
// actually issue. firstUnscheduled is the first byte after the probe body.
func parallelWorkerCount(configured int, chunk, firstUnscheduled, total int64) int {
	chunks := int64(1)
	remaining := total - firstUnscheduled
	if remaining > 0 {
		chunks += remaining / chunk
		if remaining%chunk != 0 {
			chunks++
		}
	}
	if chunks >= int64(configured) {
		return configured
	}
	return int(chunks)
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
			if !retryableRequestError(err) || attempt == attempts {
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

// dispatch feeds a fixed worker set with every chunk fetch, keeping response
// bodies plus completed buffers within workerCount. probeBody is the already-
// open response for the first returned interval.
func (p *parallelPuller) dispatch(
	c *Client,
	target *url.URL,
	probeRange contentRange,
	probeBody *trackedBody,
	probeAttempts int,
	workerCount int,
) {
	tasks := make(chan chunkTask, workerCount)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			p.work(c, target, tasks)
		}()
	}

	defer func() {
		close(tasks)
		workers.Wait()
		close(p.results)
	}()

	probeTask := chunkTask{
		index:         0,
		start:         probeRange.start,
		want:          probeRange.end - probeRange.start + 1,
		total:         probeRange.total,
		probeBody:     probeBody,
		probeAttempts: probeAttempts,
	}
	if !p.enqueueProbe(tasks, probeTask) {
		return
	}

	index := int64(1)
	chunk := c.pullChunk
	for start := probeRange.end + 1; start < probeRange.total; index++ {
		want := min(chunk, probeRange.total-start)
		if !p.enqueueChunk(tasks, chunkTask{
			index: index,
			start: start,
			want:  want,
			total: probeRange.total,
		}) {
			return
		}
		start += want
	}
}

// enqueueProbe sends the already-slot-owning probe task to a worker.
func (p *parallelPuller) enqueueProbe(tasks chan<- chunkTask, task chunkTask) bool {
	select {
	case tasks <- task:
		return true
	case <-p.ctx.Done():
		p.untrackAndClose(task.probeBody)
		p.releaseResult(chunkResult{slot: true})
		return false
	}
}

// enqueueChunk reserves one body-or-buffer slot before sending task to a
// worker. The worker's result owns the slot until the reader consumes it.
func (p *parallelPuller) enqueueChunk(tasks chan<- chunkTask, task chunkTask) bool {
	select {
	case p.slots <- struct{}{}:
	case <-p.ctx.Done():
		return false
	}
	select {
	case tasks <- task:
		return true
	case <-p.ctx.Done():
		p.releaseResult(chunkResult{slot: true})
		return false
	}
}

// work fetches tasks until dispatch closes the queue or the pull is canceled.
func (p *parallelPuller) work(c *Client, target *url.URL, tasks <-chan chunkTask) {
	for task := range tasks {
		result := p.fetchTask(c, target, task)
		result.index = task.index
		result.slot = true
		select {
		case p.results <- result:
		case <-p.ctx.Done():
			p.releaseResult(result)
		}
	}
}

// fetchTask reads the existing probe body or opens the task's ranged request.
func (p *parallelPuller) fetchTask(c *Client, target *url.URL, task chunkTask) chunkResult {
	if task.probeBody == nil {
		return c.fetchChunk(p.ctx, target, task.start, task.want, task.total, p, c.retry.attempts())
	}

	data, err := readChunkInto(p.pool, task.probeBody, task.want)
	p.untrackAndClose(task.probeBody)
	if err == nil {
		return chunkResult{data: data}
	}
	if ctxErr := p.ctx.Err(); ctxErr != nil {
		return chunkResult{err: ctxErr}
	}
	remaining := c.retry.attempts() - task.probeAttempts
	if remaining <= 0 {
		return chunkResult{err: fmt.Errorf("reading probe range: %w", err)}
	}
	return c.fetchChunk(p.ctx, target, task.start, task.want, task.total, p, remaining)
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
	data := p.pool.take()
	end := start + want - 1
	parts := 0
	for cursor := start; cursor <= end; {
		if parts == maxParallelRangeParts {
			p.pool.put(data)
			return chunkResult{err: fmt.Errorf(
				"fetching chunk bytes=%d-%d: registry split the range across more than %d partial responses",
				start, end, maxParallelRangeParts)}
		}
		var next int64
		var err error
		data, next, err = c.fetchRangePart(ctx, target, cursor, end, total, data, p, attempts)
		if err != nil {
			p.pool.put(data)
			return chunkResult{err: err}
		}
		cursor = next
		parts++
	}
	return chunkResult{data: data}
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
			data: data, err: fmt.Errorf("fetching chunk %s: %w", rangeHeader, err),
			retryable: ctx.Err() == nil && retryableRequestError(err),
		}
	}
	body, tracked := p.trackBody(resp.Body)
	if !tracked {
		return rangePartResult{data: data, err: errParallelPullClosed}
	}
	resp.Body = body
	defer p.untrackAndClose(body)

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
	before := len(data)
	partLength := parsed.end - parsed.start + 1
	data, err = appendExactly(data, body, partLength)
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
func readChunkInto(pool *chunkBufferPool, r io.Reader, want int64) ([]byte, error) {
	data := pool.take()
	data, err := appendExactly(data, r, want)
	if err != nil {
		pool.put(data)
		return nil, err
	}
	return data, nil
}

// appendExactly appends want bytes without converting an unchecked
// int64 into a slice length or allocating the declared range at once. Each
// read lands directly in dst's new tail, avoiding a per-response scratch copy.
func appendExactly(dst []byte, r io.Reader, want int64) ([]byte, error) {
	if want < 0 || uint64(want) > uint64(^uint(0)>>1)-uint64(len(dst)) {
		return dst, fmt.Errorf("range length %d exceeds addressable memory", want)
	}
	target := len(dst) + int(want)
	for len(dst) < target {
		step := min(target-len(dst), parallelReadBufferSize)
		dst = growReadBuffer(dst, step, target)
		start := len(dst)
		dst = dst[:start+step]
		n, err := io.ReadFull(r, dst[start:])
		dst = dst[:start+n]
		if err != nil {
			return dst, err
		}
	}
	return dst, nil
}

// growReadBuffer ensures additional writable bytes without growing beyond the
// validated response size. Capacity expands geometrically from a small first
// read so huge declared ranges do not trigger eager allocations.
func growReadBuffer(dst []byte, additional, target int) []byte {
	if cap(dst)-len(dst) >= additional {
		return dst
	}

	needed := len(dst) + additional
	next := max(cap(dst), parallelReadBufferSize)
	next = min(next, target)
	for next < needed {
		if next > target/2 {
			next = target
			break
		}
		next *= 2
	}
	grown := make([]byte, len(dst), next)
	copy(grown, dst)
	return grown
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

// nextResult reorders fixed-worker results while allowing cancellation to win
// over an already-ready chunk.
func (p *parallelPuller) nextResult() (chunkResult, error) {
	for {
		if result, ok := p.takePending(); ok {
			if ctxErr := p.ctx.Err(); ctxErr != nil {
				p.releaseResult(result)
				return chunkResult{}, ctxErr
			}
			return result, nil
		}

		select {
		case result, ok := <-p.results:
			if !ok {
				return chunkResult{}, io.EOF
			}
			if ctxErr := p.ctx.Err(); ctxErr != nil {
				p.releaseResult(result)
				return chunkResult{}, ctxErr
			}
			if p.resultIsNext(result) {
				return result, nil
			}
			if !p.storePending(result) {
				p.releaseResult(result)
				return chunkResult{}, p.ctx.Err()
			}
		case <-p.ctx.Done():
			return chunkResult{}, p.ctx.Err()
		}
	}
}

// takePending removes the next ordered result when a worker completed it early.
func (p *parallelPuller) takePending() (chunkResult, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	result, ok := p.pending[p.next]
	if ok {
		delete(p.pending, p.next)
	}
	return result, ok
}

// resultIsNext reports whether result is ready for immediate ordered delivery.
func (p *parallelPuller) resultIsNext(result chunkResult) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return result.index == p.next
}

// storePending retains an out-of-order result unless Close already won.
func (p *parallelPuller) storePending(result chunkResult) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	p.pending[result.index] = result
	return true
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
	p.current = result.data
	p.currentSlot, p.pos = result.slot, 0
	p.next++
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
		pending := make([]chunkResult, 0, len(p.pending))
		for _, result := range p.pending {
			pending = append(pending, result)
		}
		clear(p.pending)
		p.mu.Unlock()
		p.cancel()
		for _, body := range bodies {
			_ = body.Close()
		}
		for _, result := range pending {
			p.releaseResult(result)
		}
		go p.drainResults()
	})
	return nil
}

// drainResults releases results not consumed by a racing Read.
func (p *parallelPuller) drainResults() {
	for result := range p.results {
		p.releaseResult(result)
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
	p.pool.put(result.data)
	if result.slot {
		<-p.slots
	}
}

// releaseCurrentLocked recycles the current chunk. p.mu must be held.
func (p *parallelPuller) releaseCurrentLocked() {
	p.pool.put(p.current)
	if p.currentSlot {
		<-p.slots
	}
	p.current, p.pos = nil, 0
	p.currentSlot = false
}
