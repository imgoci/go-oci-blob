package campaign

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errorRequestBody returns one causal source error to the observed transport.
type errorRequestBody struct{}

// Read reports a deterministic caller-source failure.
func (errorRequestBody) Read([]byte) (int, error) {
	return 0, errors.New("source failed")
}

// Close releases the synthetic body.
func (errorRequestBody) Close() error {
	return nil
}

// TestWireObserverRetainsOnlySafeRequestAndResponseFacts proves credentials,
// repository names, opaque query values, and Location values never enter evidence.
func TestWireObserverRetainsOnlySafeRequestAndResponseFacts(t *testing.T) {
	const (
		requestSecret  = "request-secret-value"
		locationSecret = "location-secret-value"
		headerSecret   = "header-secret-value"
		repositoryName = "private/project-name"
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location",
			"https://storage.example.invalid/object?signature="+locationSecret+"&upload=opaque")
		writer.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)
	host := strings.TrimPrefix(server.URL, "http://")
	observer := newWireObserver(host, DefaultParallelWorkers)
	client := &http.Client{Transport: observer.wrap(routeRegistry, http.DefaultTransport)}
	capture := observer.startPhase(FeatureMonolithicPush, false)

	target := server.URL + "/v2/" + repositoryName +
		"/blobs/uploads/session?state=" + requestSecret + "&digest=sha256%3Aabc"
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPut, target, strings.NewReader("payload"))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+headerSecret)
	request.Header.Set("Cookie", "session="+headerSecret)
	request.Header.Set("Referer", "https://registry.example.invalid/?token="+headerSecret)
	response, err := client.Do(request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	snapshot := capture.finish()

	require.Len(t, snapshot.Events, 1)
	event := snapshot.Events[0]
	assert.Equal(t, FeatureMonolithicPush, event.Phase)
	assert.Equal(t, routeRegistry, event.Route)
	assert.Equal(t, "registry", event.Origin)
	assert.Equal(t, http.MethodPut, event.Method)
	assert.Equal(t, "upload", event.Endpoint)
	assert.Equal(t, http.StatusAccepted, event.Status)
	assert.Equal(t, int64(len("payload")), event.RequestBytes)
	assert.Equal(t, []string{"digest", "state"}, event.QueryKeys)
	assert.Equal(t, "off-origin", event.LocationForm)
	assert.Equal(t, []string{"signature", "upload"}, event.LocationQueryKeys)
	assert.True(t, event.SensitiveHeaders)

	encoded, err := json.Marshal(snapshot.Events)
	require.NoError(t, err)
	for _, secret := range []string{requestSecret, locationSecret, headerSecret, repositoryName} {
		assert.NotContains(t, string(encoded), secret)
	}
}

// TestWireObserverClassifiesRouteSeparatelyFromOrigin ensures evidence can
// distinguish client routing from the actual request authority.
func TestWireObserverClassifiesRouteSeparatelyFromOrigin(t *testing.T) {
	registry := httptest.NewServer(http.HandlerFunc(okHandler))
	t.Cleanup(registry.Close)
	storage := httptest.NewServer(http.HandlerFunc(okHandler))
	t.Cleanup(storage.Close)
	registryHost := strings.TrimPrefix(registry.URL, "http://")
	observer := newWireObserver(registryHost, DefaultParallelWorkers)
	capture := observer.startPhase(FeatureOffOrigin, false)

	performObservedGET(t, observer.wrap(routeRegistry, http.DefaultTransport), registry.URL+"/v2/")
	performObservedGET(t, observer.wrap(routeStorage, http.DefaultTransport), storage.URL+"/blob")
	snapshot := capture.finish()

	require.Len(t, snapshot.Events, 2)
	assert.Equal(t, routeRegistry, snapshot.Events[0].Route)
	assert.Equal(t, "registry", snapshot.Events[0].Origin)
	assert.Equal(t, routeStorage, snapshot.Events[1].Route)
	assert.Equal(t, "off-origin", snapshot.Events[1].Origin)
}

// TestWireObserverDistinguishesSourceAndTransportFailures lets upload safety
// classifiers accept a proven reader-size error without accepting an unrelated
// network break.
func TestWireObserverDistinguishesSourceAndTransportFailures(t *testing.T) {
	observer := newWireObserver("registry.example.test", DefaultParallelWorkers)
	transport := observer.wrap(routeRegistry, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		_, readErr := io.ReadAll(request.Body)
		return nil, readErr
	}))
	capture := observer.startPhase(FeatureExactSize, false)
	request, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		"https://registry.example.test/v2/repo/blobs/uploads/session",
		errorRequestBody{},
	)
	require.NoError(t, err)
	_, err = transport.RoundTrip(request)
	require.Error(t, err)

	events := capture.finish().Events
	require.Len(t, events, 1)
	assert.True(t, events[0].TransportError)
	assert.True(t, events[0].SourceBodyError)
}

// TestWireObserverRangeGateProvesOverlapAndCleanup requires two response bodies
// to overlap and confirms no body remains active when the phase finishes.
func TestWireObserverRangeGateProvesOverlapAndCleanup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Range", "bytes 0-3/8")
		writer.WriteHeader(http.StatusPartialContent)
		_, err := writer.Write([]byte("data"))
		assert.NoError(t, err)
		assert.NotEmpty(t, request.Header.Get("Range"))
	}))
	t.Cleanup(server.Close)
	host := strings.TrimPrefix(server.URL, "http://")
	observer := newWireObserver(host, DefaultParallelWorkers)
	client := &http.Client{Transport: observer.wrap(routeRegistry, http.DefaultTransport)}
	capture := observer.startPhase(FeatureParallelPull, true)

	var wait sync.WaitGroup
	errors := make(chan error, 2)
	for range 2 {
		wait.Go(func() {
			request, err := http.NewRequestWithContext(
				t.Context(),
				http.MethodGet,
				server.URL+"/v2/repo/blobs/digest",
				nil,
			)
			if err != nil {
				errors <- err
				return
			}
			request.Header.Set("Range", "bytes=0-3")
			response, err := client.Do(request)
			if err != nil {
				errors <- err
				return
			}
			_, readErr := io.ReadAll(response.Body)
			closeErr := response.Body.Close()
			if readErr != nil {
				errors <- readErr
				return
			}
			if closeErr != nil {
				errors <- closeErr
			}
		})
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	snapshot := capture.finish()

	require.Len(t, snapshot.Events, 2)
	assert.GreaterOrEqual(t, snapshot.MaximumActiveRanges, int64(2))
	assert.Zero(t, snapshot.ActiveRanges)
	for _, event := range snapshot.Events {
		assert.Equal(t, http.StatusPartialContent, event.Status)
		assert.Equal(t, "bytes=0-3", event.Range)
		assert.Equal(t, "bytes 0-3/8", event.ContentRange)
	}
}

// TestLocationFactsClassifyStableForms covers the response shapes used to
// synthesize the upload Location compatibility row.
func TestLocationFactsClassifyStableForms(t *testing.T) {
	requestURL, err := url.Parse("https://registry.example.test/v2/repo/blobs/uploads/")
	require.NoError(t, err)
	tests := []struct {
		name       string
		header     string
		wantForm   string
		wantParams []string
	}{
		{name: "empty", header: "", wantForm: "", wantParams: nil},
		{
			name: "relative", header: "/session?uuid=secret&state=opaque",
			wantForm: "relative", wantParams: []string{"state", "uuid"},
		},
		{
			name: "same origin", header: "https://registry.example.test/session?uuid=secret",
			wantForm: "same-origin", wantParams: []string{"uuid"},
		},
		{
			name: "off origin", header: "https://storage.example.test/session?token=secret",
			wantForm: "off-origin", wantParams: []string{"token"},
		},
		{name: "invalid", header: "://invalid", wantForm: "invalid", wantParams: nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			form, parameters := locationFacts(test.header, requestURL, "registry.example.test:443")
			assert.Equal(t, test.wantForm, form)
			assert.Equal(t, test.wantParams, parameters)
		})
	}
}

// okHandler serves a minimal successful control response.
func okHandler(writer http.ResponseWriter, _ *http.Request) {
	writer.WriteHeader(http.StatusOK)
}

// performObservedGET executes and closes one request through transport.
func performObservedGET(t *testing.T, transport http.RoundTripper, target string) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	require.NoError(t, err)
	response, err := (&http.Client{Transport: transport}).Do(request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
}
