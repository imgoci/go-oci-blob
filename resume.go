package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// requestAttemptCounter reports how many request attempts were spent before
// an initial response body became readable.
type requestAttemptCounter interface {
	requestAttempts() int
}

// boundedReadCloser limits reads while retaining ownership of the underlying
// response body.
type boundedReadCloser struct {
	// body is the response body closed with the bounded reader.
	body io.ReadCloser
	// reader exposes at most the validated number of response bytes.
	reader io.Reader
}

// newBoundedReadCloser wraps body with a length-byte read limit.
func newBoundedReadCloser(body io.ReadCloser, length int64) *boundedReadCloser {
	return &boundedReadCloser{
		body:   body,
		reader: io.LimitReader(body, length),
	}
}

// Read reads within the configured byte limit.
func (b *boundedReadCloser) Read(p []byte) (int, error) {
	return b.reader.Read(p)
}

// Close closes the underlying response body.
func (b *boundedReadCloser) Close() error {
	return b.body.Close()
}

// resumeResponse is a validated replacement stream and its known bounds.
type resumeResponse struct {
	// body is positioned at the next undelivered blob byte.
	body io.ReadCloser
	// total is the complete representation length, or -1 when unknown.
	total int64
	// end is the exclusive end of body, or -1 when the body is unbounded.
	end int64
}

// resumeAttemptResult describes a single replacement request that either
// produced a usable body or a retryable failure.
type resumeAttemptResult struct {
	// replacement is the validated body when retry is false.
	replacement resumeResponse
	// cause is the request or response failure that warrants another attempt.
	cause error
	// retryAfter is the registry-requested wait before another attempt.
	retryAfter time.Duration
	// retry reports whether another request may recover the stream.
	retry bool
}

// resumeReader keeps a blob download alive across broken streams. On
// a mid-stream failure it reestablishes the transfer with a ranged
// GET from the last delivered byte, so the verifying reader above it
// never sees the gap and no byte is hashed twice.
type resumeReader struct {
	// readMu serializes reads while allowing Close to interrupt one.
	readMu sync.Mutex
	// mu protects the mutable stream state shared with Close.
	mu sync.Mutex
	// ctx bounds every reestablishment request.
	ctx context.Context
	// cancel stops reestablishment requests when the reader closes.
	cancel context.CancelFunc
	// client executes the ranged GETs.
	client *Client
	// target is the blob URL the download came from.
	target *url.URL
	// body is the current network stream.
	body io.ReadCloser
	// offset counts bytes already delivered to the reader above.
	offset int64
	// attempts counts every request spent on the initial stream and resumes.
	attempts int
	// bodyEnd is the exclusive end advertised for the current response, or -1
	// when the current response is an unbounded full-body stream.
	bodyEnd int64
	// total is the representation size learned from a resume response,
	// or -1 until a response provides it.
	total int64
	// closed prevents a read from reopening the stream after Close.
	closed bool
}

// newResumeReader takes ownership of body and derives a context that
// Close can cancel independently of the caller's context.
func newResumeReader(
	ctx context.Context, client *Client, target *url.URL, body io.ReadCloser,
) *resumeReader {
	readCtx, cancel := context.WithCancel(ctx)
	attempts := 1
	if counter, ok := body.(requestAttemptCounter); ok {
		attempts = max(counter.requestAttempts(), 1)
	}
	return &resumeReader{
		ctx:      readCtx,
		cancel:   cancel,
		client:   client,
		target:   target,
		body:     body,
		attempts: attempts,
		bodyEnd:  -1,
		total:    -1,
	}
}

// Read delivers bytes from the stream, transparently reconnecting
// from the current offset when the stream breaks mid-body. End of
// stream, context cancellation, and exhausted reconnect budgets
// surface unchanged.
func (r *resumeReader) Read(p []byte) (int, error) {
	r.readMu.Lock()
	defer r.readMu.Unlock()

	if len(p) == 0 {
		if err := r.stateError(); err != nil {
			return 0, err
		}
		return 0, nil
	}

	for {
		body, stateErr := r.readableBody()
		if stateErr != nil {
			return 0, stateErr
		}
		n, err := body.Read(p)
		offset, bodyEnd, total, stateErr := r.recordRead(n)
		if stateErr != nil {
			return n, stateErr
		}
		returnNow, cause := classifyResumeReadError(err, offset, bodyEnd, total)
		if returnNow {
			return n, cause
		}
		if rerr := r.reestablish(cause); rerr != nil {
			return n, rerr
		}
		if n > 0 {
			return n, nil
		}
	}
}

// classifyResumeReadError reports whether Read should return now or use the
// returned error as the cause for another request.
func classifyResumeReadError(err error, offset, bodyEnd, total int64) (bool, error) {
	if err == nil {
		return true, nil
	}
	if total >= 0 && offset >= total {
		return true, io.EOF
	}
	if !errors.Is(err, io.EOF) {
		return false, err
	}
	if total < 0 {
		return true, io.EOF
	}
	if bodyEnd >= 0 && offset < bodyEnd {
		return false, fmt.Errorf(
			"resume response ended at byte %d before advertised byte %d: %w",
			offset, bodyEnd, io.ErrUnexpectedEOF)
	}
	return false, fmt.Errorf(
		"resume response ended at byte %d before blob end %d: %w",
		offset, total, io.ErrUnexpectedEOF)
}

// readableBody returns the current body unless the stream is closed
// or its context has finished.
func (r *resumeReader) readableBody() (io.ReadCloser, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, io.ErrClosedPipe
	}
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	return r.body, nil
}

// recordRead advances the delivered offset and snapshots the current response
// bounds, returning any terminal state that raced with the body read.
func (r *resumeReader) recordRead(n int) (int64, int64, int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.offset += int64(n)
	if r.closed {
		return r.offset, r.bodyEnd, r.total, io.ErrClosedPipe
	}
	return r.offset, r.bodyEnd, r.total, r.ctx.Err()
}

// Close cancels pending requests and closes the current network
// stream. It is safe to call concurrently with Read.
func (r *resumeReader) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	body := r.body
	r.body = nil
	r.mu.Unlock()

	r.cancel()
	if body != nil {
		return body.Close()
	}
	return nil
}

// reestablish replaces the broken stream with a ranged GET from the
// current offset. A 206 continues exactly where the break happened; a
// 200 means the registry ignored the range, so the already-delivered
// prefix is discarded from the fresh stream instead. cause is the
// read error that triggered the reconnect.
func (r *resumeReader) reestablish(cause error) error {
	retryAfter := time.Duration(0)
	for {
		offset, ok, err := r.waitForResumeAttempt(retryAfter)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("blob stream kept breaking at byte %d after %d request attempts: %w",
				offset, r.client.retry.attempts(), cause)
		}

		result, err := r.executeResumeAttempt(offset, cause)
		if err != nil {
			return err
		}
		if result.retry {
			cause = result.cause
			retryAfter = result.retryAfter
			continue
		}
		return r.replaceBody(result.replacement)
	}
}

// waitForResumeAttempt reserves the next request and applies the retry delay.
// ok is false when the stream has spent its complete request budget.
func (r *resumeReader) waitForResumeAttempt(retryAfter time.Duration) (int64, bool, error) {
	offset, failedAttempt, ok, stateErr := r.reserveResumeAttempt()
	if stateErr != nil || !ok {
		return offset, ok, stateErr
	}
	if err := sleepContext(
		r.ctx, r.client.retry.backoffDelay(failedAttempt, retryAfter),
	); err != nil {
		if stateErr := r.stateError(); stateErr != nil {
			return offset, false, stateErr
		}
		return offset, false, fmt.Errorf("waiting to resume blob download at byte %d: %w", offset, err)
	}
	return offset, true, nil
}

// executeResumeAttempt performs and validates one replacement request.
func (r *resumeReader) executeResumeAttempt(
	offset int64, cause error,
) (resumeAttemptResult, error) {
	resp, err := r.requestResume(offset)
	if err != nil {
		if stateErr := r.stateError(); stateErr != nil {
			return resumeAttemptResult{}, stateErr
		}
		if !retryableRequestError(err) {
			return resumeAttemptResult{}, fmt.Errorf(
				"resuming blob download at byte %d (cause: %w): %w", offset, cause, err)
		}
		return resumeAttemptResult{cause: err, retry: true}, nil
	}
	if retryableStatus(resp.StatusCode) {
		result := resumeAttemptResult{
			cause:      interpretError(resp),
			retryAfter: retryAfterDelay(resp.Header.Get("Retry-After"), time.Now()),
			retry:      true,
		}
		drainAndClose(resp.Body)
		return result, nil
	}

	replacement, err := r.validateResumeResponse(resp, offset, cause)
	if err != nil {
		return resumeAttemptResult{}, err
	}
	return resumeAttemptResult{replacement: replacement}, nil
}

// reserveResumeAttempt spends one request from the lifetime budget and returns
// the byte offset plus the ordinal of the failed attempt that precedes it.
func (r *resumeReader) reserveResumeAttempt() (int64, int, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return r.offset, r.attempts, false, io.ErrClosedPipe
	}
	if err := r.ctx.Err(); err != nil {
		return r.offset, r.attempts, false, err
	}
	if r.attempts >= r.client.retry.attempts() {
		return r.offset, r.attempts, false, nil
	}
	failedAttempt := r.attempts
	r.attempts++
	return r.offset, failedAttempt, true, nil
}

// requestResume performs one replacement request beginning at offset. Retry
// ownership stays with resumeReader so nested request loops cannot multiply
// the configured attempt budget.
func (r *resumeReader) requestResume(offset int64) (*http.Response, error) {
	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, r.target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("building resume request: %w", err)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	resp, err := r.client.doRegistryRequest(req)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, contextOperationError(r.ctx, err)
	}
	return resp, nil
}

// validateResumeResponse verifies a replacement begins at offset, positions a
// full-body fallback at that byte, or confirms terminal EOF with an exact 416
// total. A partial body is capped at its advertised interval so excess bytes
// cannot cross a response boundary.
func (r *resumeReader) validateResumeResponse(
	resp *http.Response, offset int64, cause error,
) (resumeResponse, error) {
	switch resp.StatusCode {
	case http.StatusPartialContent:
		parsed, ok := parseContentRange(resp.Header.Get("Content-Range"))
		if !ok {
			_ = resp.Body.Close()
			return resumeResponse{}, fmt.Errorf(
				"resuming blob download at byte %d: registry returned invalid Content-Range %q",
				offset, resp.Header.Get("Content-Range"))
		}
		if parsed.start != offset {
			_ = resp.Body.Close()
			return resumeResponse{}, fmt.Errorf(
				"resuming blob download at byte %d: registry returned Content-Range %q",
				offset, resp.Header.Get("Content-Range"))
		}
		knownTotal := r.knownTotal()
		if knownTotal >= 0 && parsed.total != knownTotal {
			_ = resp.Body.Close()
			return resumeResponse{}, fmt.Errorf(
				"resuming blob download at byte %d: blob size changed from %d to %d",
				offset, knownTotal, parsed.total)
		}
		return resumeResponse{
			body:  newBoundedReadCloser(resp.Body, parsed.end-parsed.start+1),
			total: parsed.total,
			end:   parsed.end + 1,
		}, nil
	case http.StatusRequestedRangeNotSatisfiable:
		return r.validateTerminalResumeResponse(resp, offset)
	case http.StatusOK:
		if _, err := io.CopyN(io.Discard, resp.Body, offset); err != nil {
			_ = resp.Body.Close()
			if stateErr := r.stateError(); stateErr != nil {
				return resumeResponse{}, stateErr
			}
			return resumeResponse{}, fmt.Errorf("resuming blob download at byte %d (cause: %w): "+
				"registry ignored the range and the replayed stream ended early: %w",
				offset, cause, contextOperationError(r.ctx, err))
		}
		if stateErr := r.stateError(); stateErr != nil {
			_ = resp.Body.Close()
			return resumeResponse{}, stateErr
		}
		knownTotal := r.knownTotal()
		if knownTotal >= 0 {
			return resumeResponse{
				body:  newBoundedReadCloser(resp.Body, knownTotal-offset),
				total: knownTotal,
				end:   knownTotal,
			}, nil
		}
		return resumeResponse{body: resp.Body, total: -1, end: -1}, nil
	default:
		responseErr := interpretError(resp)
		_ = resp.Body.Close()
		return resumeResponse{}, fmt.Errorf("resuming blob download at byte %d (cause: %w): %w",
			offset, cause, responseErr)
	}
}

// validateTerminalResumeResponse accepts a 416 as EOF evidence only when its
// unsatisfied-range total matches the delivered offset and any known total.
func (r *resumeReader) validateTerminalResumeResponse(
	resp *http.Response, offset int64,
) (resumeResponse, error) {
	header := resp.Header.Get("Content-Range")
	total := unsatisfiedRangeTotal(header)
	drainAndClose(resp.Body)
	if total < 0 {
		return resumeResponse{}, fmt.Errorf(
			"resuming blob download at byte %d: registry returned invalid Content-Range %q",
			offset, header)
	}
	if total != offset {
		return resumeResponse{}, fmt.Errorf(
			"resuming blob download at byte %d: registry reported unsatisfied range total %d",
			offset, total)
	}
	knownTotal := r.knownTotal()
	if knownTotal >= 0 && total != knownTotal {
		return resumeResponse{}, fmt.Errorf(
			"resuming blob download at byte %d: blob size changed from %d to %d",
			offset, knownTotal, total)
	}
	return resumeResponse{body: http.NoBody, total: total, end: total}, nil
}

// knownTotal returns the representation length learned from an earlier 206.
func (r *resumeReader) knownTotal() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.total
}

// replaceBody atomically installs replacement unless Close or context
// completion won the race, then closes the superseded response.
func (r *resumeReader) replaceBody(replacement resumeResponse) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		_ = replacement.body.Close()
		return io.ErrClosedPipe
	}
	if err := r.ctx.Err(); err != nil {
		r.mu.Unlock()
		_ = replacement.body.Close()
		return err
	}
	oldBody := r.body
	r.body = replacement.body
	r.bodyEnd = replacement.end
	if replacement.total >= 0 {
		r.total = replacement.total
	}
	r.mu.Unlock()

	_ = oldBody.Close()
	return nil
}

// stateError reports closure or parent context completion without
// replacing those conditions with a stale stream error.
func (r *resumeReader) stateError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return io.ErrClosedPipe
	}
	return r.ctx.Err()
}
