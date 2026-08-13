package campaign

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	blob "github.com/imgoci/go-oci-blob"
)

// TestClassifyManifestLinkResponseSeparatesRegistryPolicyFromInfrastructure
// locks the only safe empty-blob NO path after a nil Push result.
func TestClassifyManifestLinkResponseSeparatesRegistryPolicyFromInfrastructure(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		want      manifestLinkOutcome
		wantError bool
	}{
		{name: "created", status: http.StatusCreated, want: manifestLinkSucceeded},
		{
			name: "referenced blob missing", status: http.StatusBadRequest,
			body: `{"errors":[{"code":"MANIFEST_BLOB_UNKNOWN"}]}`,
			want: manifestLinkBlobMissing,
		},
		{name: "auth failure", status: http.StatusUnauthorized, wantError: true},
		{name: "transient failure", status: http.StatusInternalServerError, wantError: true},
		{
			name: "unrelated refusal", status: http.StatusBadRequest,
			body:      `{"errors":[{"code":"MANIFEST_INVALID"}]}`,
			wantError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := classifyManifestLinkResponse(test.status, []byte(test.body))
			if test.wantError {
				require.Error(t, err)
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

// roundTripFunc adapts a function into a deterministic HTTP transport.
type roundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip delegates one request to function.
func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

// TestClassifyChunkedRejectsTransportFailureAsInconclusive ensures network
// failure cannot become a reproducible registry incompatibility.
func TestClassifyChunkedRejectsTransportFailureAsInconclusive(t *testing.T) {
	runner := newWriteClassifierRunner(t, http.StatusBadRequest, "", nil)
	value := newFixture(runner.cfg.Run.ID, "chunked", 4099)
	wire := WireSnapshot{Events: []WireEvent{
		{Method: http.MethodPatch, Endpoint: endpointUpload, TransportError: true},
		{Method: http.MethodDelete, Endpoint: endpointUpload, Status: http.StatusNoContent},
	}}

	result, err := runner.classifyChunked(t.Context(), value, wire, errors.New("connection reset"))

	require.Error(t, err)
	assert.Empty(t, result.Status)
}

// TestValidMonolithicWireRequiresARealZeroByteUpload locks the wire half of the
// empty-blob PASS contract against globally virtualized empty digests.
func TestValidMonolithicWireRequiresARealZeroByteUpload(t *testing.T) {
	valid := []WireEvent{
		{Method: http.MethodPost, Endpoint: endpointUpload, Status: http.StatusAccepted},
		{
			Method: http.MethodPut, Endpoint: endpointUpload,
			Status: http.StatusCreated, RequestBytes: 0,
		},
	}
	require.True(t, validMonolithicWire(valid, 0))

	tests := []struct {
		name   string
		events []WireEvent
	}{
		{name: "missing session POST", events: valid[1:]},
		{name: "missing commit PUT", events: valid[:1]},
		{name: "PATCH is not monolithic", events: append(append([]WireEvent{}, valid...), WireEvent{
			Method: http.MethodPatch, Endpoint: endpointUpload, Status: http.StatusAccepted,
		})},
		{name: "POST was not accepted", events: []WireEvent{
			{Method: http.MethodPost, Endpoint: endpointUpload, Status: http.StatusCreated},
			valid[1],
		}},
		{name: "PUT was not created", events: []WireEvent{
			valid[0],
			{Method: http.MethodPut, Endpoint: endpointUpload, Status: http.StatusAccepted, RequestBytes: 0},
		}},
		{name: "unknown PUT length", events: []WireEvent{
			valid[0],
			{Method: http.MethodPut, Endpoint: endpointUpload, Status: http.StatusCreated, RequestBytes: -1},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.False(t, validMonolithicWire(test.events, 0))
		})
	}
}

// TestWrongDigestRequiresCausalNonRetryableRefusal distinguishes a protocol
// rejection from retry exhaustion while keeping the candidate digests absent.
func TestWrongDigestRequiresCausalNonRetryableRefusal(t *testing.T) {
	tests := []struct {
		name         string
		uploadStatus int
		wantStatus   Status
		wantError    bool
	}{
		{name: "terminal 400 proves rejection", uploadStatus: http.StatusBadRequest, wantStatus: StatusPass},
		{name: "retryable 500 is inconclusive", uploadStatus: http.StatusInternalServerError, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := newWriteClassifierRunner(t, test.uploadStatus, "", nil)

			result, err := runner.probeWrongDigest(t.Context())

			if test.wantError {
				require.Error(t, err)
				assert.Empty(t, result.Status)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.wantStatus, result.Status)
		})
	}
}

// TestWrongDigestPersistenceRequiresExpectedBytes prevents an unrelated 200
// response from becoming unsafe-persistence evidence.
func TestWrongDigestPersistenceRequiresExpectedBytes(t *testing.T) {
	const runID = "write-classifier-run"
	value := newFixture(runID, "wrong-digest", 3101)
	claimed := newFixture(runID, "wrong-digest-claim", 3101).digest
	tests := []struct {
		name       string
		body       []byte
		wantStatus Status
		wantError  bool
	}{
		{name: "unrelated success body is inconclusive", body: []byte("proxy success page"), wantError: true},
		{name: "exact uploaded bytes prove unsafe persistence", body: value.data, wantStatus: StatusNo},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := newWriteClassifierRunner(t, http.StatusBadRequest, claimed.String(), test.body)

			result, err := runner.probeWrongDigest(t.Context())

			if test.wantError {
				require.Error(t, err)
				assert.Empty(t, result.Status)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.wantStatus, result.Status)
		})
	}
}

// TestExactSizeRecognitionIgnoresIncidentalWords prevents repository or
// transport text containing "size" from becoming reader-size evidence.
func TestExactSizeRecognitionIgnoresIncidentalWords(t *testing.T) {
	assert.True(t, isExactSizeError(errors.New(
		"pushing blob: reader size does not match declared size: yielded 3 bytes, expected 4",
	)))
	assert.False(t, isExactSizeError(errors.New(
		"pushing blob to compat/exact-size-run: connection reset",
	)))
}

// TestExactSizeRejectsAuthAndTransientWireOutcomes keeps infrastructure
// failures from supporting an exact-size PASS.
func TestExactSizeRejectsAuthAndTransientWireOutcomes(t *testing.T) {
	assert.True(t, hasAuthOrTransientUploadFailure([]WireEvent{{
		Method: http.MethodPut, Endpoint: endpointUpload, Status: http.StatusInternalServerError,
	}}))
	assert.False(t, hasAuthOrTransientUploadFailure([]WireEvent{{
		Method: http.MethodPut, Endpoint: endpointUpload, TransportError: true, SourceBodyError: true,
	}}), "a causal short reader can surface as a transport error")
	assert.True(t, hasAuthOrTransientUploadFailure([]WireEvent{{
		Method: http.MethodPut, Endpoint: endpointUpload, TransportError: true,
	}}), "an unrelated transport failure must invalidate the classification")
	assert.False(t, hasAuthOrTransientUploadFailure([]WireEvent{{
		Method: http.MethodPut, Endpoint: endpointUpload, Status: http.StatusCreated,
	}}))
	assert.False(t, hasAuthOrTransientUploadFailure([]WireEvent{{
		Method: http.MethodDelete, Endpoint: endpointUpload, Status: http.StatusInternalServerError,
	}}), "best-effort cleanup cannot erase causal reader-size evidence")
}

// newWriteClassifierRunner builds one consumer-facing upload runner over
// deterministic registry and independent-control transports.
func newWriteClassifierRunner(
	t *testing.T,
	uploadStatus int,
	visibleDigest string,
	visibleBody []byte,
) *campaignRunner {
	t.Helper()
	const (
		host       = "registry.example.test"
		repository = "compat/source"
		runID      = "write-classifier-run"
	)
	observer := newWireObserver(host, DefaultParallelWorkers)
	registry := observer.wrap(routeRegistry, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Body != nil {
			_, _ = io.Copy(io.Discard, request.Body)
			_ = request.Body.Close()
		}
		status := http.StatusNoContent
		header := make(http.Header)
		switch request.Method {
		case http.MethodPost:
			status = http.StatusAccepted
			header.Set("Location", "/v2/"+repository+"/blobs/uploads/session")
		case http.MethodPut:
			status = uploadStatus
		case http.MethodDelete:
			status = http.StatusNoContent
		default:
			require.Failf(t, "unexpected registry method", "got %s", request.Method)
		}
		return testHTTPResponse(request, status, header, nil), nil
	}))
	control := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		status := http.StatusNotFound
		var body []byte
		if visibleDigest != "" && strings.HasSuffix(request.URL.Path, "/"+visibleDigest) {
			status = http.StatusOK
			if request.Method == http.MethodGet {
				body = visibleBody
			}
		}
		return testHTTPResponse(request, status, nil, body), nil
	})
	client := blob.New(
		blob.WithTransport(registry),
		blob.WithStorageTransport(registry),
		blob.WithRetryPolicy(blob.RetryPolicy{}),
	)
	return &campaignRunner{
		cfg: Config{
			Registry: RegistryConfig{Host: host},
			Run:      RunConfig{ID: runID, SourceRepository: repository},
		},
		durations: durations{absenceSettle: time.Nanosecond},
		observer:  observer,
		raw:       &http.Client{Transport: control},
		source:    blob.Repository{Host: host, Name: repository},
		serial:    client,
	}
}

// testHTTPResponse constructs one closed-world response for scripted transports.
func testHTTPResponse(
	request *http.Request,
	status int,
	header http.Header,
	body []byte,
) *http.Response {
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		StatusCode:    status,
		Status:        http.StatusText(status),
		Header:        header,
		Body:          io.NopCloser(strings.NewReader(string(body))),
		ContentLength: int64(len(body)),
		Request:       request,
	}
}
