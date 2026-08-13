package blob

import (
	"errors"
	"net/http"
	"time"
)

// retryableError preserves the fact that a fresh operation may succeed.
type retryableError struct {
	// err is the underlying operation failure.
	err error
	// after is the peer-requested minimum delay before another attempt.
	after time.Duration
}

// Error renders the underlying operation failure unchanged.
func (e *retryableError) Error() string {
	return e.err.Error()
}

// Unwrap exposes the underlying operation failure.
func (e *retryableError) Unwrap() error {
	return e.err
}

// terminalError records that caller cancellation makes the operation terminal,
// even when a nested transport cause would otherwise be retryable.
type terminalError struct {
	// err is the terminal operation failure.
	err error
}

// Error renders the underlying terminal failure unchanged.
func (e *terminalError) Error() string {
	return e.err.Error()
}

// Unwrap exposes the underlying terminal failure.
func (e *terminalError) Unwrap() error {
	return e.err
}

// markRetryable attaches outer-attempt retry metadata to err.
func markRetryable(err error, after time.Duration) error {
	if err == nil {
		return nil
	}
	return &retryableError{err: err, after: after}
}

// markTerminal records that err must not be retried by an outer orchestrator.
func markTerminal(err error) error {
	if err == nil {
		return nil
	}
	return &terminalError{err: err}
}

// Retryable reports whether a fresh operation may succeed after err.
//
// When ok is true, after is the minimum delay requested by the peer through
// Retry-After, or zero when the peer requested no usable delay. The result
// survives contextual wrapping and retry-policy exhaustion, including the
// one-attempt policy selected by WithRetryPolicy(RetryPolicy{}).
//
// Retryable describes a fresh operation by an embedding orchestrator. It does
// not change the client's built-in retry table or guarantee that the current
// request body can be replayed.
func Retryable(err error) (time.Duration, bool) {
	if err == nil {
		return 0, false
	}
	var terminal *terminalError
	if errors.As(err, &terminal) {
		return 0, false
	}
	var marked *retryableError
	if errors.As(err, &marked) {
		return marked.after, true
	}
	var responseErr *registryError
	if !errors.As(err, &responseErr) {
		return 0, false
	}
	if retryableResponse(responseErr.origin, responseErr.status) {
		return responseErr.retryAfter, true
	}
	return 0, false
}

// StatusCode returns the HTTP response status retained by err.
func StatusCode(err error) (int, bool) {
	var responseErr *registryError
	if !errors.As(err, &responseErr) {
		return 0, false
	}
	return responseErr.status, true
}

// retryableResponse reports whether a fresh operation may recover from an HTTP
// response at origin.
func retryableResponse(origin responseOrigin, status int) bool {
	if retryableStatus(status) {
		return true
	}
	if origin != responseOriginStorage {
		return false
	}
	switch status {
	case http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusGone:
		return true
	default:
		return false
	}
}
