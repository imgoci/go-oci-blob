package blob

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
)

const (
	// maxConsecutiveEmptyReads bounds tolerance for discouraged but valid
	// [io.Reader] calls that return no bytes and no error.
	maxConsecutiveEmptyReads = 100
	// uploadBodyBufferSize batches caller-source reads while bounding read-ahead
	// when a registry stops consuming an upload early.
	uploadBodyBufferSize = 256 * 1024
	// uploadBodyBufferCount keeps source and transport runnable independently
	// while capping each request body's staged bytes at one MiB.
	uploadBodyBufferCount = 4
	// retainedUploadBodyBufferCount caps idle full-size staging buffers at two
	// MiB across the process; excess concurrent buffers return to the heap.
	retainedUploadBodyBufferCount = 8
)

// uploadBodyBufferPool reuses a bounded number of full-size staging buffers.
var uploadBodyBufferPool = make(chan []byte, retainedUploadBodyBufferCount) //nolint:gochecknoglobals // process pool

// uploadBody batches source reads so Close can unblock transport reads while
// Push waits separately for caller-source ownership to end.
type uploadBody struct {
	// stateMu protects ownership state and channel closure.
	stateMu sync.Mutex
	// exact enforces the declared body length and optional final EOF.
	exact *exactSizeReader
	// chunks queues staged source batches for the transport reader.
	chunks chan uploadBodyChunk
	// available returns consumed staging buffers to the source pump.
	available chan []byte
	// buffers owns every staging allocation until request ownership is released.
	buffers [uploadBodyBufferCount][]byte
	// bufferCount is the number of entries in buffers owned by this body.
	bufferCount int
	// closed broadcasts that transport ownership has ended.
	closed chan struct{}
	// start starts at most one source-pump goroutine on the first Read.
	start sync.Once
	// released closes once Close was called and any active source read exited.
	released chan struct{}
	// readMu serializes transport reads and protects the current staged batch.
	readMu sync.Mutex
	// current is the staged batch being consumed by the transport.
	current []byte
	// currentOffset is the next unread byte in current.
	currentOffset int
	// currentErr is published after every byte in current has been consumed.
	currentErr error
	// closeCalled rejects reads after transport ownership has ended.
	closeCalled bool
	// readActive records that one transport Read may still mutate body state.
	readActive bool
	// pumpStarted records that a goroutine may still be accessing exact.
	pumpStarted bool
	// pumpDone records that the source-pump goroutine has exited.
	pumpDone bool
	// written is the number of source bytes the transport actually consumed.
	written atomic.Int64
	// releaseCalled prevents released from being closed twice.
	releaseCalled bool
}

// uploadBodyChunk carries one staged source batch and any terminal error
// following its final byte.
type uploadBodyChunk struct {
	// data is the staged source batch owned by the transport reader.
	data []byte
	// err is returned after data has been consumed.
	err error
}

// newUploadBody wraps exact in an ownership-aware request body.
func newUploadBody(exact *exactSizeReader) *uploadBody {
	return &uploadBody{
		exact:     exact,
		chunks:    make(chan uploadBodyChunk, uploadBodyBufferCount),
		available: make(chan []byte, uploadBodyBufferCount),
		closed:    make(chan struct{}),
		released:  make(chan struct{}),
	}
}

// Read starts the bounded source pump and serves its staged batches.
func (b *uploadBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	b.readMu.Lock()
	defer b.readMu.Unlock()
	if !b.beginRead() {
		b.recycleCurrent()
		return 0, http.ErrBodyReadAfterClose
	}
	defer b.finishRead()
	b.start.Do(b.startPump)
	for {
		if b.currentOffset < len(b.current) {
			b.stateMu.Lock()
			if b.closeCalled {
				b.stateMu.Unlock()
				return 0, http.ErrBodyReadAfterClose
			}
			n := copy(p, b.current[b.currentOffset:])
			b.currentOffset += n
			b.written.Add(int64(n))
			b.stateMu.Unlock()
			var terminalErr error
			if b.currentOffset == len(b.current) {
				terminalErr = b.currentErr
				b.recycleCurrent()
			}
			return n, terminalErr
		}
		select {
		case <-b.closed:
			return 0, http.ErrBodyReadAfterClose
		case chunk := <-b.chunks:
			select {
			case <-b.closed:
				return 0, http.ErrBodyReadAfterClose
			default:
			}
			if len(chunk.data) == 0 {
				return 0, chunk.err
			}
			b.current = chunk.data
			b.currentOffset = 0
			b.currentErr = chunk.err
		}
	}
}

// Close ends transport ownership promptly without closing a potentially
// blocked caller source.
func (b *uploadBody) Close() error {
	b.stateMu.Lock()
	if b.closeCalled {
		b.stateMu.Unlock()
		return nil
	}
	b.closeCalled = true
	close(b.closed)
	b.releaseIfIdleLocked()
	b.stateMu.Unlock()
	if b.readMu.TryLock() {
		b.recycleCurrent()
		b.readMu.Unlock()
	}
	return nil
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

// pump queues bounded source batches while the request body retains ownership
// of every staging buffer.
func (b *uploadBody) pump() {
	if b.exact.expected <= 0 {
		select {
		case b.chunks <- uploadBodyChunk{err: io.EOF}:
		case <-b.closed:
		}
		b.stateMu.Lock()
		b.pumpDone = true
		b.releaseIfIdleLocked()
		b.stateMu.Unlock()
		return
	}
	bufferSize := min(int64(uploadBodyBufferSize), b.exact.expected)
	bufferCount := min(uploadBodyBufferCount, int(1+(b.exact.expected-1)/bufferSize))
	b.stateMu.Lock()
	b.bufferCount = bufferCount
	for i := range bufferCount {
		b.buffers[i] = acquireUploadBodyBuffer(int(bufferSize))
		b.available <- b.buffers[i]
	}
	b.stateMu.Unlock()
	defer func() {
		b.stateMu.Lock()
		b.pumpDone = true
		b.releaseIfIdleLocked()
		b.stateMu.Unlock()
	}()
	for {
		var buffer []byte
		select {
		case buffer = <-b.available:
		case <-b.closed:
			return
		}
		n, closed, err := b.fillBuffer(buffer)
		if closed {
			return
		}
		if n == 0 {
			select {
			case b.chunks <- uploadBodyChunk{err: err}:
			case <-b.closed:
			}
			return
		}
		select {
		case b.chunks <- uploadBodyChunk{data: buffer[:n], err: err}:
		case <-b.closed:
			return
		}
		if err != nil {
			return
		}
	}
}

// fillBuffer waits through empty reads but publishes first progress so a
// demand-driven producer cannot deadlock.
func (b *uploadBody) fillBuffer(buffer []byte) (int, bool, error) {
	for {
		select {
		case <-b.closed:
			return 0, true, nil
		default:
		}
		n, err := b.exact.Read(buffer)
		if n > 0 || err != nil {
			return n, false, err
		}
	}
}

// acquireUploadBodyBuffer returns a staging buffer, reusing only full-size
// buffers from the bounded process pool.
func acquireUploadBodyBuffer(size int) []byte {
	if size == uploadBodyBufferSize {
		select {
		case buffer := <-uploadBodyBufferPool:
			return buffer
		default:
		}
	}
	return make([]byte, size)
}

// releaseUploadBodyBuffer retains a full-size buffer only when the bounded
// process pool has capacity.
func releaseUploadBodyBuffer(buffer []byte) {
	if cap(buffer) != uploadBodyBufferSize {
		return
	}
	select {
	case uploadBodyBufferPool <- buffer[:uploadBodyBufferSize]:
	default:
	}
}

// beginRead records one active transport read unless Close ended ownership.
func (b *uploadBody) beginRead() bool {
	b.stateMu.Lock()
	defer b.stateMu.Unlock()
	if b.closeCalled {
		return false
	}
	b.readActive = true
	return true
}

// finishRead releases staged bytes after a concurrent Close and publishes
// stable transport state.
func (b *uploadBody) finishRead() {
	b.stateMu.Lock()
	if b.closeCalled {
		b.recycleCurrent()
	}
	b.readActive = false
	b.releaseIfIdleLocked()
	b.stateMu.Unlock()
}

// recycleCurrent returns the consumed or abandoned staging buffer.
func (b *uploadBody) recycleCurrent() {
	if b.current == nil {
		return
	}
	buffer := b.current[:cap(b.current)]
	b.current = nil
	b.currentOffset = 0
	b.currentErr = nil
	select {
	case b.available <- buffer:
	case <-b.closed:
	}
}

// releaseIfIdleLocked publishes stable source state once all owners finish.
// stateMu must be held.
func (b *uploadBody) releaseIfIdleLocked() {
	if b.closeCalled && (!b.pumpStarted || b.pumpDone) && !b.readActive {
		b.releaseLocked()
	}
}

// releaseLocked publishes stable exact-reader state. stateMu must be held.
func (b *uploadBody) releaseLocked() {
	if !b.releaseCalled {
		b.releaseCalled = true
		for i := range b.bufferCount {
			releaseUploadBodyBuffer(b.buffers[i])
			b.buffers[i] = nil
		}
		b.bufferCount = 0
		close(b.released)
	}
}

// waitReleased waits until Push no longer accesses the caller's reader.
func (b *uploadBody) waitReleased() {
	<-b.released
}

// sourceErrorIfReleased reports a proven source error after its state is stable.
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
	written := b.written.Load()
	if written != b.exact.expected {
		return fmt.Errorf(
			"reader size does not match declared size: request consumed %d bytes, expected %d",
			b.exact.offset+written, b.exact.offset+b.exact.expected)
	}
	return b.exact.validate()
}

// exactSizeReader exposes at most the declared byte count and records a short
// source as an input error.
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

// validateReaderEOF checks for bytes after a size another path consumed.
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
