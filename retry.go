package blob

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RetryPolicy bounds how the client retries failed requests.
//
// The zero value means a single attempt with no retries. Retries
// trigger on connection errors, request timeouts, 429, and 5xx; other
// 4xx statuses mean the request is wrong, not unlucky, and are never
// retried. The caller's context bounds retry scheduling: a canceled context
// stops new attempts and backoff immediately.
type RetryPolicy struct {
	// MaxAttempts is the total number of tries for one operation,
	// including the first. Values below one behave as one.
	MaxAttempts int

	// InitialDelay seeds the exponential backoff: attempt n waits a
	// full-jittered duration in [0, InitialDelay * 2^(n-1)].
	InitialDelay time.Duration

	// MaxDelay caps every wait, including waits requested by a
	// registry's Retry-After header.
	MaxDelay time.Duration
}

// Default retry policy values applied by New when the caller does not
// override the policy.
const (
	defaultMaxAttempts  = 4
	defaultInitialDelay = 250 * time.Millisecond
	defaultMaxDelay     = 30 * time.Second
)

// DefaultRetryPolicy returns the policy New applies when
// WithRetryPolicy is not given: four total attempts, backoff seeded
// at 250ms, and no wait longer than 30 seconds.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:  defaultMaxAttempts,
		InitialDelay: defaultInitialDelay,
		MaxDelay:     defaultMaxDelay,
	}
}

// WithRetryPolicy replaces the client's retry policy. Pass a zero
// RetryPolicy to disable retries entirely.
func WithRetryPolicy(policy RetryPolicy) Option {
	return func(o *options) {
		o.retry = policy
	}
}

// attempts returns the effective total number of tries.
func (p RetryPolicy) attempts() int {
	return max(p.MaxAttempts, 1)
}

// backoffDelay computes the full-jittered wait before the retry that
// follows failed attempt number attempt (1-based). When the registry
// asked for a specific wait via Retry-After, that wish wins the
// jitter but still respects MaxDelay.
func (p RetryPolicy) backoffDelay(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return min(retryAfter, max(p.MaxDelay, 0))
	}
	ceiling := p.InitialDelay << (attempt - 1)
	// Guard both the shift overflowing and InitialDelay outgrowing
	// MaxDelay.
	if ceiling > p.MaxDelay || ceiling <= 0 {
		ceiling = p.MaxDelay
	}
	if ceiling <= 0 {
		return 0
	}
	return rand.N(ceiling) //nolint:gosec // backoff jitter needs no cryptographic randomness
}

// retryableStatus reports whether a response status warrants another
// attempt: 429 and the whole 5xx family.
func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests ||
		(code >= http.StatusInternalServerError && code <= 599)
}

// retryAfterDelay parses a Retry-After header, which carries either a
// number of seconds or an HTTP date. Zero means no usable wish.
func retryAfterDelay(header string, now time.Time) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	if seconds, err := strconv.ParseUint(header, 10, 64); err == nil {
		const maxDuration = time.Duration(1<<63 - 1)
		if seconds > uint64(maxDuration/time.Second) {
			return maxDuration
		}
		return time.Duration(seconds) * time.Second
	} else if strings.Trim(header, "0123456789") == "" {
		// A syntactically valid delay-seconds value that exceeds uint64
		// still means "wait a very long time". Saturating lets MaxDelay
		// cap it instead of wrapping the duration or ignoring the header.
		return time.Duration(1<<63 - 1)
	}
	if at, err := http.ParseTime(header); err == nil {
		return at.Sub(now)
	}
	return 0
}

// sleepContext waits for d or until ctx is canceled, whichever comes
// first.
func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// doRetry executes requests built by build until one yields a
// non-retryable outcome or the policy is exhausted.
//
// It returns the response whenever one was obtained on the final
// try — success and non-retryable statuses alike — leaving status
// interpretation to the caller. Responses consumed by intermediate
// retries are drained and closed here. build runs once per attempt so
// every try gets a fresh request.
func (c *Client) doRetry(ctx context.Context, build func() (*http.Request, error)) (*http.Response, error) {
	attempts := c.retry.attempts()
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result := c.doRetryAttempt(ctx, build, attempt == attempts)
		if !result.retry {
			return result.response, result.err
		}
		if sleepErr := sleepContext(ctx, c.retry.backoffDelay(attempt, result.retryAfter)); sleepErr != nil {
			return nil, retryCanceledError(sleepErr, result.err)
		}
	}
}

// retryAttemptResult describes one request outcome and any next-attempt wait.
type retryAttemptResult struct {
	// response is returned to the caller for a terminal HTTP outcome.
	response *http.Response
	// retryAfter is the registry's requested delay before another attempt.
	retryAfter time.Duration
	// retry reports whether the caller should consume another attempt.
	retry bool
	// err is a terminal error or the transport error that led to a retry.
	err error
}

// doRetryAttempt executes one request and reports whether its outcome should
// consume another attempt after retryAfter.
func (c *Client) doRetryAttempt(
	ctx context.Context, build func() (*http.Request, error), last bool,
) retryAttemptResult {
	req, err := build()
	if err != nil {
		return retryAttemptResult{err: err}
	}
	resp, err := c.doRegistryRequest(req) //nolint:bodyclose // terminal responses belong to doRetry callers
	if err != nil {
		if ctx.Err() != nil {
			return retryAttemptResult{err: contextOperationError(ctx, err)}
		}
		if !retryableRequestError(err) || last {
			return retryAttemptResult{err: err}
		}
		return retryAttemptResult{retry: true, err: err}
	}
	if !retryableStatus(resp.StatusCode) || last {
		return retryAttemptResult{response: resp}
	}
	retryAfter := retryAfterDelay(resp.Header.Get("Retry-After"), time.Now())
	drainAndClose(resp.Body)
	return retryAttemptResult{retryAfter: retryAfter, retry: true}
}

// retryCanceledError keeps both cancellation and the last transport error
// inspectable when a retry wait is interrupted.
func retryCanceledError(cancelErr, lastErr error) error {
	if lastErr != nil {
		return fmt.Errorf("retry canceled: %w (last attempt: %w)", cancelErr, lastErr)
	}
	return fmt.Errorf("retry canceled: %w", cancelErr)
}

// contextOperationError keeps the caller's cancellation or deadline
// inspectable while retaining the operation error that exposed it.
func contextOperationError(ctx context.Context, operationErr error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%w (operation failed with: %w)", ctxErr, operationErr)
	}
	return operationErr
}

// drainAndClose discards a bounded amount of a response body and
// closes it, keeping the underlying connection reusable.
func drainAndClose(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxErrorBodySize))
	_ = body.Close()
}
