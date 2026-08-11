package perf_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"

	blob "github.com/imgoci/go-oci-blob"
	"github.com/imgoci/go-oci-blob/mocks"
)

// testContext bundles the generated transport mock with a transfer client.
type testContext struct {
	// transport scripts the registry conversation.
	transport *mocks.RoundTripper
	// client exercises only the public transfer API.
	client *blob.Client
}

// newTestContext creates an isolated, single-attempt transfer client.
func newTestContext(t *testing.T) *testContext {
	t.Helper()

	transport := mocks.NewRoundTripper(t)
	return &testContext{
		transport: transport,
		client: blob.New(
			blob.WithTransport(transport),
			blob.WithStorageTransport(transport),
			blob.WithRetryPolicy(blob.RetryPolicy{}),
		),
	}
}

// response creates the minimal registry response required by transfer tests.
func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{},
	}
}

// sessionResponse creates an upload-session response with a Location header.
func sessionResponse(status int, location string) *http.Response {
	resp := response(status, "")
	if location != "" {
		resp.Header.Set("Location", location)
	}
	return resp
}

// postRequestFor matches a POST request by its complete URL.
func postRequestFor(urlStr string) any {
	return mock.MatchedBy(func(req *http.Request) bool {
		return req.Method == http.MethodPost && req.URL.String() == urlStr
	})
}
