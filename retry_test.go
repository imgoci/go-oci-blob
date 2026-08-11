package blob

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRetryPolicyBackoffDelay(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts:  5,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     time.Second,
	}

	t.Run("jitters within the exponential ceiling", func(t *testing.T) {
		for range 200 {
			delay := policy.backoffDelay(2, 0)
			assert.GreaterOrEqual(t, delay, time.Duration(0))
			assert.Less(t, delay, 200*time.Millisecond, "attempt 2 ceiling is Initial*2")
		}
	})

	t.Run("caps the ceiling at MaxDelay for late attempts", func(t *testing.T) {
		for range 200 {
			delay := policy.backoffDelay(30, 0)
			assert.Less(t, delay, time.Second)
		}
	})

	t.Run("honors a Retry-After wish below the cap", func(t *testing.T) {
		assert.Equal(t, 500*time.Millisecond, policy.backoffDelay(1, 500*time.Millisecond))
	})

	t.Run("caps a Retry-After wish at MaxDelay", func(t *testing.T) {
		assert.Equal(t, time.Second, policy.backoffDelay(1, time.Minute))
		assert.Equal(t, time.Second, policy.backoffDelay(1, time.Duration(1<<63-1)),
			"a saturated Retry-After must still honor MaxDelay")
	})

	t.Run("waits nothing under a zero policy", func(t *testing.T) {
		assert.Equal(t, time.Duration(0), RetryPolicy{}.backoffDelay(1, 0))
	})
}

func TestRetryAfterDelay(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{name: "parses seconds", header: "7", want: 7 * time.Second},
		{
			name:   "parses an HTTP date",
			header: now.Add(90 * time.Second).Format(http.TimeFormat),
			want:   90 * time.Second,
		},
		{name: "ignores an empty header", header: "", want: 0},
		{name: "ignores garbage", header: "soon", want: 0},
		{
			name:   "saturates seconds that would overflow a duration",
			header: "9223372037",
			want:   time.Duration(1<<63 - 1),
		},
		{
			name:   "saturates a decimal value larger than uint64",
			header: "18446744073709551616",
			want:   time.Duration(1<<63 - 1),
		},
		{name: "ignores a negative delay", header: "-1", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, retryAfterDelay(tt.header, now))
		})
	}
}
