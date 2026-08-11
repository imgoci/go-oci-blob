package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opencontainers/go-digest"
)

const (
	// maxConsecutiveEmptyReads bounds tolerance for discouraged but valid
	// [io.Reader] calls that return no bytes and no error.
	maxConsecutiveEmptyReads = 100
	// uploadBodyBufferSize bounds the copy buffer that decouples transport
	// cancellation from a potentially blocked caller reader.
	uploadBodyBufferSize = 32 * 1024
)

// Push uploads a blob monolithically: one POST to open an upload
// session, one PUT to send the bytes and commit them under dgst.
// With [WithChunkedUpload] the bytes travel in verified PATCH chunks
// instead; see that option for why chunked stays opt-in.
//
// The size is mandatory and must match the number of bytes r yields;
// registries need it as Content-Length, and there is no unknown-length
// upload. A caller that does not know the size spools the data first
// and comes back with a number. The reader must reach EOF immediately
// after size bytes; a streaming producer must close its pipe so Push can
// prove there is no trailing data. The registry rejects the commit when
// the content does not hash to dgst, so a corrupt stream cannot be stored
// silently.
//
// Push does not access r after returning. To preserve that ownership rule,
// it waits for every in-flight r.Read call to finish even after an early
// registry response or context cancellation. A reader that can block must
// arrange for its producer to unblock it when ctx ends; an arbitrary
// [io.Reader] has no general cancellation operation.
//
// A failed upload restarts from byte zero under the client's
// [RetryPolicy]. Restarting needs the reader's bytes again, so r must
// implement [io.Seeker] (as [bytes.Reader], [strings.Reader], and
// [os.File] do); with a non-seekable reader the upload fails on the
// first transient error instead of retrying.
//
// Example:
//
//	dgst := digest.FromBytes(data)
//	err := client.Push(ctx, repo, dgst, int64(len(data)), bytes.NewReader(data))
func (c *Client) Push(
	ctx context.Context,
	repo Repository,
	dgst digest.Digest,
	size int64,
	r io.Reader,
	opts ...TransferOption,
) error {
	if err := validateTarget(repo, dgst); err != nil {
		return err
	}
	if size < 0 {
		return fmt.Errorf("size %d is invalid: pushes require the exact blob size", size)
	}
	if nilReader(r) {
		return errors.New("nil reader")
	}
	if isNilValue(ctx) {
		return errors.New("nil context")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("pushing blob: %w", err)
	}
	cfg := applyTransferOptions(opts)
	tracker := newProgressTracker(cfg.progress, size)
	replay, err := newReaderReplay(r)
	if err != nil {
		return fmt.Errorf("capturing reader position for upload retries: %w", err)
	}
	if size == 0 {
		if err := validateReaderEOF(r, 0); err != nil {
			return err
		}
	}
	return c.pushWithRetry(ctx, repo, dgst, size, r, tracker, replay)
}

// pushWithRetry runs complete upload attempts and rewinds the caller's reader
// between retryable failures.
func (c *Client) pushWithRetry(
	ctx context.Context, repo Repository, dgst digest.Digest, size int64, r io.Reader,
	tracker *progressTracker, replay *readerReplay,
) error {
	uploadOnce := c.pushOnce
	if c.chunkSize > 0 {
		uploadOnce = c.chunkedOnce
	}

	attempts := c.retry.attempts()
	for attempt := 1; ; attempt++ {
		retryable, err := uploadOnce(ctx, repo, dgst, size, r, tracker, replay)
		if err == nil {
			return nil
		}
		err = fmt.Errorf("pushing blob %s to %s/%s: %w", dgst, repo.Host, repo.Name, err)
		if ctx.Err() != nil {
			return contextOperationError(ctx, err)
		}
		if !retryable || attempt == attempts {
			return err
		}
		if size > 0 {
			if replay == nil {
				return fmt.Errorf(
					"%w (upload restart needs the bytes again, but the reader is not an io.Seeker)", err)
			}
			if rerr := replay.rewind(); rerr != nil {
				return fmt.Errorf("rewinding reader to restart upload: %w (restart cause: %w)", rerr, err)
			}
		}
		if sleepErr := sleepContext(ctx, c.retry.backoffDelay(attempt, retryAfterOf(err))); sleepErr != nil {
			return fmt.Errorf("upload retry canceled: %w (last attempt: %w)", sleepErr, err)
		}
	}
}

// pushOnce runs one POST+PUT monolithic upload attempt. The bool
// reports whether the failure is worth restarting the upload for:
// transport-level breaks and retryable statuses are, everything else
// is not.
func (c *Client) pushOnce(
	ctx context.Context, repo Repository, dgst digest.Digest, size int64, r io.Reader,
	tracker *progressTracker, replay *readerReplay,
) (bool, error) {
	session, retryable, err := c.openSession(ctx, repo, "")
	if err != nil {
		return retryable, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = c.cancelUploadSession(ctx, session)
		}
	}()

	retryable, err = c.commitUpload(ctx, session, dgst, size, r, replay)
	if err != nil {
		return retryable, err
	}
	// A monolithic attempt has no committed bytes until the registry
	// accepts the final PUT. Failed attempts therefore report nothing.
	committed = true
	tracker.set(size)
	return false, nil
}

// uploadSession is an open blob upload session on a registry.
type uploadSession struct {
	// url is the session URL the next upload request must target.
	url *url.URL
	// registry is the originating registry endpoint. It remains stable
	// when a session Location points at another authority.
	registry *url.URL
	// minChunk is the registry's OCI-Chunk-Min-Length wish in bytes,
	// or zero when the registry stated none.
	minChunk int64
}

// openSession opens an upload session with POST
// /v2/<name>/blobs/uploads/ and returns it. The bool carries the same
// restartability meaning as pushOnce's.
func (c *Client) openSession(
	ctx context.Context, repo Repository, algorithm digest.Algorithm,
) (*uploadSession, bool, error) {
	target := &url.URL{
		Scheme: c.scheme(),
		Host:   repo.Host,
		Path:   "/v2/" + repo.Name + "/blobs/uploads/",
	}
	if algorithm != "" && algorithm != digest.SHA256 {
		target.RawQuery = "digest-algorithm=" + url.QueryEscape(algorithm.String())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), nil)
	if err != nil {
		return nil, false, fmt.Errorf("building session request: %w", err)
	}
	resp, err := c.doRegistryRequest(req)
	if err != nil {
		return nil, retryableRequestError(err), fmt.Errorf("starting upload session: %w", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		defer resp.Body.Close()
		regErr := interpretError(resp)
		return nil, retryableRegistryStatus(regErr), fmt.Errorf("starting upload session: %w", regErr)
	}
	drainAndClose(resp.Body)

	sessionURL, err := resolveLocation(responseRequestURL(resp, target), resp.Header.Get("Location"))
	if err != nil {
		return nil, false, fmt.Errorf("starting upload session: %w", err)
	}
	// The spec spells the header OCI-Chunk-Min-Length; Go canonicalizes
	// the key either way, and header names are case-insensitive on the
	// wire.
	minChunk, _ := strconv.ParseInt(resp.Header.Get("Oci-Chunk-Min-Length"), 10, 64)
	return &uploadSession{url: sessionURL, registry: target, minChunk: minChunk}, false, nil
}

// commitUpload sends the blob bytes with PUT <session>?digest=<digest>
// and checks that the registry accepted them. The bool carries the
// same restartability meaning as pushOnce's.
func (c *Client) commitUpload(
	ctx context.Context, session *uploadSession, dgst digest.Digest, size int64, r io.Reader,
	replay *readerReplay,
) (bool, error) {
	uploadURL := withDigest(session.url, dgst)
	req, initialBody, err := newCommitRequest(ctx, uploadURL, size, r, replay)
	if err != nil {
		return false, err
	}

	resp, err := c.doLocationRequest(req, session.registry)
	if err != nil {
		return commitRequestError(err, initialBody, replay)
	}
	if size > 0 && replay == nil && replayRedirect(resp.StatusCode) {
		_, locationErr := resolveLocation(
			responseRequestURL(resp, uploadURL), resp.Header.Get("Location"))
		drainAndClose(resp.Body)
		body := currentUploadBody(initialBody, replay)
		body.waitReleased()
		if sourceErr := body.sourceErrorIfReleased(); sourceErr != nil {
			return false, fmt.Errorf("committing upload: %w", sourceErr)
		}
		if locationErr != nil {
			return false, fmt.Errorf("committing upload: %w: %w", errInvalidRedirectTarget, locationErr)
		}
		return false, fmt.Errorf(
			"committing upload: registry returned %d but the reader is not an io.Seeker, "+
				"so the request body cannot be replayed",
			resp.StatusCode)
	}
	if resp.StatusCode != http.StatusCreated {
		return rejectCommitResponse(resp, session, uploadURL, initialBody, replay)
	}
	drainAndClose(resp.Body)
	body := currentUploadBody(initialBody, replay)
	if body != nil {
		body.waitReleased()
		if err := body.validate(); err != nil {
			return false, fmt.Errorf("committing upload: %w", err)
		}
	}
	return false, nil
}

// newCommitRequest builds a final PUT with the exact Content-Length and body
// replay hooks required by OCI and net/http redirects.
func newCommitRequest(
	ctx context.Context, uploadURL *url.URL, size int64, r io.Reader, replay *readerReplay,
) (*http.Request, *uploadBody, error) {
	requestBody := io.Reader(http.NoBody)
	var initialBody *uploadBody
	if size > 0 {
		initialBody = newUploadBody(newExactSizeReader(r, size, true))
		requestBody = initialBody
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL.String(), requestBody)
	if err != nil {
		return nil, nil, fmt.Errorf("building commit request: %w", err)
	}
	if replay != nil && initialBody != nil {
		if err := replay.register(initialBody); err != nil {
			return nil, nil, fmt.Errorf("registering commit request body: %w", err)
		}
		req.GetBody = replay.getBody(replay.start, 0, size, true)
	}
	// NewRequest only infers ContentLength for a few concrete reader types.
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")
	return req, initialBody, nil
}

// commitRequestError keeps a proven source error ahead of a secondary
// transport failure, then classifies redirect-policy failures as nonretryable.
func commitRequestError(
	err error, initialBody *uploadBody, replay *readerReplay,
) (bool, error) {
	body := currentUploadBody(initialBody, replay)
	if body != nil {
		body.waitReleased()
		if sourceErr := body.sourceErrorIfReleased(); sourceErr != nil {
			return false, fmt.Errorf("committing upload: %w", sourceErr)
		}
	}
	return retryableRequestError(err), fmt.Errorf("committing upload: %w", err)
}

// rejectCommitResponse interprets a non-201 response and retains a 202
// session Location solely for best-effort cleanup.
func rejectCommitResponse(
	resp *http.Response,
	session *uploadSession,
	uploadURL *url.URL,
	initialBody *uploadBody,
	replay *readerReplay,
) (bool, error) {
	var locationErr error
	if resp.StatusCode == http.StatusAccepted {
		var next *url.URL
		next, locationErr = resolveLocation(
			responseRequestURL(resp, uploadURL), resp.Header.Get("Location"))
		if locationErr == nil {
			session.url = next
		}
	}
	regErr := interpretError(resp)
	_ = resp.Body.Close()
	body := currentUploadBody(initialBody, replay)
	if body != nil {
		body.waitReleased()
		if sourceErr := body.sourceErrorIfReleased(); sourceErr != nil {
			return false, fmt.Errorf("committing upload: %w", sourceErr)
		}
	}
	if locationErr != nil {
		return false, fmt.Errorf(
			"committing upload: %w (could not resolve upload-session Location for cleanup: %w)",
			regErr, locationErr)
	}
	return retryableRegistryStatus(regErr), fmt.Errorf("committing upload: %w", regErr)
}

// replayRedirect reports a redirect that preserves the request method and
// therefore requires net/http to replay a non-empty request body.
func replayRedirect(status int) bool {
	return status == http.StatusTemporaryRedirect || status == http.StatusPermanentRedirect
}

// readerReplay captures a seekable reader's starting position so retries and
// 307/308 redirects can replay a request body without buffering it.
type readerReplay struct {
	// mu serializes request-body ownership and seeks on seeker.
	mu sync.Mutex
	// seeker is the caller's reader with seeking support.
	seeker io.ReadSeeker
	// start is the reader position at the beginning of Push.
	start int64
	// current is the request body that most recently owned seeker.
	current *uploadBody
}

// newReaderReplay captures r's current position, or returns nil when r cannot
// seek. An initial Seek failure is returned instead of being mistaken for a
// non-seekable reader later.
func newReaderReplay(r io.Reader) (*readerReplay, error) {
	seeker, ok := r.(io.ReadSeeker)
	if !ok {
		return nil, nil //nolint:nilnil // nil replay means the reader cannot seek
	}
	start, err := seeker.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, err
	}
	return &readerReplay{seeker: seeker, start: start}, nil
}

// rewind returns the reader to the position captured when Push began.
func (r *readerReplay) rewind() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.waitCurrentReleased(); err != nil {
		return err
	}
	_, err := r.seeker.Seek(r.start, io.SeekStart)
	return err
}

// position reports the seekable reader's current offset.
func (r *readerReplay) position() (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.waitCurrentReleased(); err != nil {
		return 0, err
	}
	return r.seeker.Seek(0, io.SeekCurrent)
}

// register records body as the current owner of the shared seeker after the
// previous request body has released it.
func (r *readerReplay) register(body *uploadBody) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.waitCurrentReleased(); err != nil {
		return err
	}
	r.current = body
	return nil
}

// waitCurrentReleased waits for the transport's ownership handoff and keeps a
// newly proven source error from being erased by a seek or replay.
// r.mu must be held.
func (r *readerReplay) waitCurrentReleased() error {
	if r.current == nil {
		return nil
	}
	r.current.waitReleased()
	if sourceErr := r.current.sourceErrorIfReleased(); sourceErr != nil {
		return sourceErr
	}
	return nil
}

// active returns the request body that most recently owned the seeker.
func (r *readerReplay) active() *uploadBody {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current
}

// getBody builds the replay hook net/http needs for 307 and 308 redirects. It
// waits for net/http to close the previous body before seeking the shared
// reader, matching the RoundTripper body-ownership contract.
func (r *readerReplay) getBody(
	start, offset, size int64, requireEOF bool,
) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if err := r.waitCurrentReleased(); err != nil {
			return nil, err
		}
		if _, err := r.seeker.Seek(start, io.SeekStart); err != nil {
			return nil, fmt.Errorf("rewinding redirected upload body: %w", err)
		}
		exact := newExactSizeReader(r.seeker, size, requireEOF)
		exact.offset = offset
		body := newUploadBody(exact)
		r.current = body
		return body, nil
	}
}

// currentUploadBody returns the request body whose state is authoritative
// after the final redirect hop.
func currentUploadBody(initial *uploadBody, replay *readerReplay) *uploadBody {
	if replay == nil {
		return initial
	}
	return replay.active()
}

// uploadBody streams through a pipe so Close unblocks transport reads while
// replay and Push can wait separately for caller-source ownership to end.
type uploadBody struct {
	// stateMu protects ownership state and channel closure.
	stateMu sync.Mutex
	// exact enforces the declared body length and optional final EOF.
	exact *exactSizeReader
	// reader is the transport-facing half of the cancellation-aware pipe.
	reader *io.PipeReader
	// writer receives bytes pumped from exact.
	writer *io.PipeWriter
	// start starts at most one source-pump goroutine on the first Read.
	start sync.Once
	// released closes once Close was called and any active source read exited.
	released chan struct{}
	// closeCalled rejects reads after transport ownership has ended.
	closeCalled bool
	// pumpStarted records that a goroutine may still be accessing exact.
	pumpStarted bool
	// pumpDone records that the source-pump goroutine has exited.
	pumpDone bool
	// written is the number of source bytes the transport actually consumed.
	written int64
	// releaseCalled prevents released from being closed twice.
	releaseCalled bool
}

// newUploadBody wraps exact in an ownership-aware request body.
func newUploadBody(exact *exactSizeReader) *uploadBody {
	reader, writer := io.Pipe()
	return &uploadBody{
		exact: exact, reader: reader, writer: writer, released: make(chan struct{}),
	}
}

// Read starts the bounded source pump and reads its transport-facing pipe.
func (b *uploadBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	b.start.Do(b.startPump)
	return b.reader.Read(p)
}

// Close marks transport ownership ended without waiting on a potentially
// blocked caller source. It is idempotent and leaves that source open.
func (b *uploadBody) Close() error {
	b.stateMu.Lock()
	if b.closeCalled {
		b.stateMu.Unlock()
		return nil
	}
	b.closeCalled = true
	if !b.pumpStarted || b.pumpDone {
		b.releaseLocked()
	}
	b.stateMu.Unlock()
	return b.reader.CloseWithError(http.ErrBodyReadAfterClose)
}

// startPump begins one streaming copy unless Close won the first-Read race.
func (b *uploadBody) startPump() {
	b.stateMu.Lock()
	if b.closeCalled {
		b.stateMu.Unlock()
		return
	}
	b.pumpStarted = true
	b.stateMu.Unlock()
	go b.pump()
}

// pump copies the exact-size source through the request pipe and publishes
// stable validation state before releasing caller-source ownership.
func (b *uploadBody) pump() {
	buffer := make([]byte, uploadBodyBufferSize)
	written, err := io.CopyBuffer(b.writer, b.exact, buffer)
	if err != nil {
		_ = b.writer.CloseWithError(err)
	} else {
		_ = b.writer.Close()
	}

	b.stateMu.Lock()
	b.written = written
	b.pumpDone = true
	if b.closeCalled {
		b.releaseLocked()
	}
	b.stateMu.Unlock()
}

// releaseLocked publishes stable exact-reader state. stateMu must be held.
func (b *uploadBody) releaseLocked() {
	if !b.releaseCalled {
		b.releaseCalled = true
		close(b.released)
	}
}

// waitReleased waits until no transport-owned read can still mutate source
// state. It deliberately outlives context cancellation to guarantee Push never
// accesses the caller's reader after returning.
func (b *uploadBody) waitReleased() {
	<-b.released
}

// sourceErrorIfReleased reports a proven source error only when its state is
// already stable; an in-flight blocked read deliberately leaves it unknown.
func (b *uploadBody) sourceErrorIfReleased() error {
	select {
	case <-b.released:
		return b.exact.sizeErr
	default:
		return nil
	}
}

// validate checks body state after waitReleased has established ownership.
func (b *uploadBody) validate() error {
	if b.exact.sizeErr != nil {
		return b.exact.sizeErr
	}
	if b.written != b.exact.expected {
		return fmt.Errorf(
			"reader size does not match declared size: request consumed %d bytes, expected %d",
			b.exact.offset+b.written, b.exact.offset+b.exact.expected)
	}
	return b.exact.validate()
}

// exactSizeReader exposes at most the declared number of bytes while
// recording a short source stream as an input error.
type exactSizeReader struct {
	// reader is the caller's source stream.
	reader io.Reader
	// expected is the declared byte count.
	expected int64
	// offset is the number of blob bytes accepted before this request body.
	offset int64
	// remaining is the number of declared bytes not yet read.
	remaining int64
	// sizeErr records a proven short-stream mismatch.
	sizeErr error
	// requireEOF makes the final declared byte also prove source EOF.
	requireEOF bool
	// eofValidated records that the source ended exactly at expected.
	eofValidated bool
	// emptyReads counts consecutive source Reads that made no progress.
	emptyReads int
}

// newExactSizeReader wraps reader with an exact declared byte count.
func newExactSizeReader(reader io.Reader, size int64, requireEOF bool) *exactSizeReader {
	return &exactSizeReader{
		reader: reader, expected: size, remaining: size, requireEOF: requireEOF,
	}
}

// validateReaderEOF checks that reader has no bytes after a declared size
// that another request path already consumed.
func validateReaderEOF(reader io.Reader, size int64) error {
	exact := newExactSizeReader(reader, size, true)
	if err := exact.probeEOF(); err != nil {
		return err
	}
	return exact.validate()
}

// Read forwards no more than the declared number of bytes and turns an early
// EOF into a stable size-mismatch error.
func (r *exactSizeReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.reader.Read(p)
	if n < 0 || n > len(p) {
		r.sizeErr = fmt.Errorf(
			"reading declared upload body: reader returned invalid byte count %d for buffer length %d",
			n, len(p))
		return 0, r.sizeErr
	}
	if n == 0 && err == nil {
		r.emptyReads++
		if r.emptyReads >= maxConsecutiveEmptyReads {
			r.sizeErr = fmt.Errorf("reading declared upload body: %w", io.ErrNoProgress)
			return 0, r.sizeErr
		}
		return 0, nil
	}
	r.emptyReads = 0
	r.remaining -= int64(n)
	if err != nil && !errors.Is(err, io.EOF) {
		r.sizeErr = fmt.Errorf("reading declared upload body: %w", err)
		return n, r.sizeErr
	}
	if errors.Is(err, io.EOF) && r.remaining > 0 {
		r.sizeErr = fmt.Errorf(
			"reader size does not match declared size: yielded %d bytes, expected %d",
			r.offset+r.expected-r.remaining, r.offset+r.expected)
		return n, r.sizeErr
	}
	if r.remaining == 0 && r.requireEOF {
		if errors.Is(err, io.EOF) {
			r.eofValidated = true
		} else if probeErr := r.probeEOF(); probeErr != nil {
			return n, probeErr
		}
	}
	return n, err
}

// validate checks that the transport consumed every declared byte and, for a
// final body, observed EOF immediately afterward.
func (r *exactSizeReader) validate() error {
	if r.sizeErr != nil {
		return r.sizeErr
	}
	if r.remaining != 0 {
		return fmt.Errorf(
			"reader size does not match declared size: request consumed %d bytes, expected %d",
			r.offset+r.expected-r.remaining, r.offset+r.expected)
	}
	if r.requireEOF && !r.eofValidated {
		return fmt.Errorf("reader size does not match declared size: EOF after %d bytes was not observed",
			r.offset+r.expected)
	}
	return nil
}

// probeEOF reads one byte beyond the declared body to distinguish exact EOF
// from trailing source data.
func (r *exactSizeReader) probeEOF() error {
	for range maxConsecutiveEmptyReads {
		var extra [1]byte
		n, err := r.reader.Read(extra[:])
		if n > 0 {
			r.sizeErr = fmt.Errorf(
				"reader size does not match declared size: source contains data after %d bytes",
				r.offset+r.expected)
			return r.sizeErr
		}
		if errors.Is(err, io.EOF) {
			r.eofValidated = true
			return nil
		}
		if err != nil {
			r.sizeErr = fmt.Errorf("checking reader against declared size: %w", err)
			return r.sizeErr
		}
	}
	r.sizeErr = fmt.Errorf("checking reader against declared size: %w", io.ErrNoProgress)
	return r.sizeErr
}

// withDigest returns a copy of uploadURL with its digest query field added or
// replaced. Every non-digest RawQuery byte remains unchanged because session
// state can be opaque and semicolon-bearing.
func withDigest(uploadURL *url.URL, dgst digest.Digest) *url.URL {
	result := *uploadURL
	escapedDigest := url.QueryEscape(dgst.String())
	parts := strings.Split(result.RawQuery, "&")
	replaced := false
	for i, part := range parts {
		key, _, _ := strings.Cut(part, "=")
		decodedKey, err := url.QueryUnescape(key)
		if err != nil || decodedKey != "digest" {
			continue
		}
		parts[i] = key + "=" + escapedDigest
		replaced = true
	}
	if replaced {
		result.RawQuery = strings.Join(parts, "&")
		return &result
	}
	if result.RawQuery == "" {
		result.RawQuery = "digest=" + escapedDigest
	} else {
		result.RawQuery += "&digest=" + escapedDigest
	}
	return &result
}

// nilReader catches both a nil interface and an interface holding a typed nil
// pointer before invoking its Read or Seek method.
func nilReader(r io.Reader) bool {
	return isNilValue(r)
}

// responseRequestURL returns the final request URL after redirects, falling
// back to the original URL for synthetic responses without Request metadata.
func responseRequestURL(resp *http.Response, fallback *url.URL) *url.URL {
	if resp != nil && resp.Request != nil && resp.Request.URL != nil {
		return resp.Request.URL
	}
	return fallback
}

// cancelUploadSession deletes an abandoned upload session. It keeps request
// values but outlives caller cancellation long enough to clean up registry
// state. Callers with a primary upload failure may intentionally ignore the
// returned cleanup error.
func (c *Client) cancelUploadSession(ctx context.Context, session *uploadSession) error {
	const timeout = 5 * time.Second
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cleanupCtx, http.MethodDelete, session.url.String(), nil)
	if err != nil {
		return fmt.Errorf("building upload-session cancellation request: %w", err)
	}
	resp, err := c.doLocationRequest(req, session.registry)
	if err != nil {
		return fmt.Errorf("canceling upload session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("canceling upload session: %w", interpretError(resp))
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBodySize))
	return nil
}

// retryableRegistryStatus reports whether err carries a registry
// status worth retrying.
func retryableRegistryStatus(err error) bool {
	var regErr *registryError
	return errors.As(err, &regErr) && retryableStatus(regErr.status)
}

// retryAfterOf extracts a registry's Retry-After wish from err, or
// zero when it carries none.
func retryAfterOf(err error) time.Duration {
	var regErr *registryError
	if errors.As(err, &regErr) {
		return regErr.retryAfter
	}
	return 0
}
