package campaign

import (
	"errors"
	"io"
	"net/http"
	"sync"
)

// bodyFault injects one mid-stream failure after a known delivered prefix.
type bodyFault struct {
	// once selects exactly one eligible full response.
	once sync.Once
	// cutoff is the number of bytes delivered before failure.
	cutoff int64
}

// faultRoundTripper injects a shared bodyFault after next receives a response.
type faultRoundTripper struct {
	// next performs the actual request.
	next http.RoundTripper
	// fault is shared by registry and storage routes.
	fault *bodyFault
}

// RoundTrip wraps the first full successful blob GET body.
func (transport *faultRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.next.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if request.Method == http.MethodGet && request.Header.Get("Range") == "" &&
		response.StatusCode == http.StatusOK {
		transport.fault.once.Do(func() {
			response.Body = &failingBody{body: response.Body, remaining: transport.fault.cutoff}
		})
	}
	return response, nil
}

// failingBody exposes a prefix, then returns one synthetic transport failure.
type failingBody struct {
	// body is the real response body.
	body io.ReadCloser
	// remaining is the prefix still allowed through.
	remaining int64
	// failed reports that the synthetic error was returned.
	failed bool
}

// Read returns the allowed prefix followed by [io.ErrUnexpectedEOF].
func (body *failingBody) Read(buffer []byte) (int, error) {
	if body.failed {
		return 0, io.ErrUnexpectedEOF
	}
	if body.remaining == 0 {
		body.failed = true
		return 0, io.ErrUnexpectedEOF
	}
	if int64(len(buffer)) > body.remaining {
		buffer = buffer[:body.remaining]
	}
	count, err := body.body.Read(buffer)
	body.remaining -= int64(count)
	if body.remaining == 0 && (err == nil || errors.Is(err, io.EOF)) {
		body.failed = true
		return count, io.ErrUnexpectedEOF
	}
	return count, err
}

// Close closes the underlying network response.
func (body *failingBody) Close() error {
	return body.body.Close()
}
