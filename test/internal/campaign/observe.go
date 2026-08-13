package campaign

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

// transportRoute identifies which public client transport received a request.
type transportRoute string

const (
	// routeRegistry is the authenticated registry-origin route.
	routeRegistry transportRoute = "registry"
	// routeStorage is the credential-free off-origin route.
	routeStorage transportRoute = "storage"
	// originRegistry identifies the configured registry origin.
	originRegistry = "registry"
	// originOffRegistry identifies any other HTTP origin.
	originOffRegistry = "off-origin"
	// endpointUpload identifies upload-session endpoints.
	endpointUpload = "upload"
	// locationInvalid identifies an unparsable Location.
	locationInvalid = "invalid"
	// locationRelative identifies a relative Location.
	locationRelative = "relative"
	// locationSameOrigin identifies an absolute registry Location.
	locationSameOrigin = "same-origin"
)

// WireEvent is the deliberately lossy, secret-safe description of one request.
type WireEvent struct {
	// Phase identifies the currently executing feature probe.
	Phase string `json:"phase"`
	// Route is registry or storage.
	Route transportRoute `json:"route"`
	// Origin reports whether the actual request matched the registry authority.
	Origin string `json:"origin"`
	// Method is the HTTP request method.
	Method string `json:"method"`
	// Endpoint is a broad path category; it never contains a repository name.
	Endpoint string `json:"endpoint"`
	// Status is zero when the transport failed before receiving a response.
	Status int `json:"status"`
	// Range is the non-secret byte range request value.
	Range string `json:"range,omitempty"`
	// ContentRange is the registry's range acknowledgement.
	ContentRange string `json:"content_range,omitempty"`
	// ResponseRange is the upload server's acknowledged inclusive range.
	ResponseRange string `json:"response_range,omitempty"`
	// RequestBytes is the declared request size, or -1 when unknown.
	RequestBytes int64 `json:"request_bytes"`
	// QueryKeys records parameter names without values.
	QueryKeys []string `json:"query_keys,omitempty"`
	// LocationForm is relative, same-origin, off-origin, invalid, or empty.
	LocationForm string `json:"location_form,omitempty"`
	// LocationQueryKeys records only response Location parameter names.
	LocationQueryKeys []string `json:"location_query_keys,omitempty"`
	// SensitiveHeaders reports a credential-bearing request header by presence.
	SensitiveHeaders bool `json:"sensitive_headers"`
	// TransportError reports failure without retaining an error string or URL.
	TransportError bool `json:"transport_error"`
	// SourceBodyError reports that the request body independently exposed a
	// causal reader error before the transport returned.
	SourceBodyError bool `json:"source_body_error"`
}

// WireSnapshot is the evidence recorded during one bounded probe phase.
type WireSnapshot struct {
	// Events is an independent copy of the phase's safe request facts.
	Events []WireEvent
	// MaximumActiveRanges is the largest number of simultaneously open 206 bodies.
	MaximumActiveRanges int64
	// ActiveRanges is the number of ranged response bodies left open at phase end.
	ActiveRanges int64
}

// wireObserver coordinates phase labels, events, and optional parallel gates.
type wireObserver struct {
	// registryHost is the canonical expected authority.
	registryHost string
	// workers bounds how many ranged bodies may participate in a gate.
	workers int
	// mu protects phase, events, and gate.
	mu sync.Mutex
	// phase is copied into new events.
	phase string
	// events contains secret-safe observations for the complete run.
	events []WireEvent
	// gate is non-nil only during a deliberately parallel probe.
	gate *rangeGate
}

// phaseCapture marks an observer interval with optional deterministic overlap.
type phaseCapture struct {
	// observer owns the recorded events.
	observer *wireObserver
	// start is the first event index in this interval.
	start int
	// gate records response-body concurrency for this interval.
	gate *rangeGate
}

// rangeGate holds the first reads of ranged bodies until two workers overlap.
type rangeGate struct {
	// target is the number of bodies needed to release the gate.
	target int64
	// active counts entered bodies that have not reached EOF or Close.
	active atomic.Int64
	// maximum is the high-water active-body count.
	maximum atomic.Int64
	// ready closes once target active bodies have entered.
	ready chan struct{}
	// readyOnce protects ready from duplicate close.
	readyOnce sync.Once
	// stopped closes when the phase ends, releasing an incomplete gate.
	stopped chan struct{}
	// stopOnce protects stopped from duplicate close.
	stopOnce sync.Once
}

// observedBody tracks body lifetime and optionally synchronizes its first read.
type observedBody struct {
	// body is the response body owned by the caller.
	body io.ReadCloser
	// requestDone releases a blocked first read if its context ends.
	requestDone <-chan struct{}
	// gate is the phase-local parallel synchronization point.
	gate *rangeGate
	// entered protects the one-time active count and wait.
	entered sync.Once
	// left protects the one-time active decrement.
	left sync.Once
}

// observingRoundTripper records one library-visible transport route.
type observingRoundTripper struct {
	// route is registry or storage.
	route transportRoute
	// next performs the request.
	next http.RoundTripper
	// observer receives safe request and response facts.
	observer *wireObserver
}

// observedRequestBody records whether a request body returned an error while
// preserving the original body's close and replay semantics.
type observedRequestBody struct {
	// body is the caller-owned request body.
	body io.ReadCloser
	// failed is shared with the wire event recorder.
	failed *atomic.Bool
}

// newWireObserver creates an empty campaign observer.
func newWireObserver(registryHost string, workers int) *wireObserver {
	return &wireObserver{registryHost: canonicalAuthority(registryHost, "https"), workers: workers}
}

// wrap returns a route-aware recorder around next.
func (observer *wireObserver) wrap(route transportRoute, next http.RoundTripper) http.RoundTripper {
	return &observingRoundTripper{route: route, next: next, observer: observer}
}

// startPhase labels subsequent events and optionally gates parallel range reads.
func (observer *wireObserver) startPhase(name string, gateRanges bool) *phaseCapture {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	var gate *rangeGate
	if gateRanges {
		gate = &rangeGate{target: 2, ready: make(chan struct{}), stopped: make(chan struct{})}
	}
	observer.phase = name
	observer.gate = gate
	return &phaseCapture{observer: observer, start: len(observer.events), gate: gate}
}

// finish ends a phase, releases any incomplete gate, and returns its evidence.
func (capture *phaseCapture) finish() WireSnapshot {
	if capture.gate != nil {
		capture.gate.stopOnce.Do(func() { close(capture.gate.stopped) })
	}
	capture.observer.mu.Lock()
	defer capture.observer.mu.Unlock()
	if capture.observer.gate == capture.gate {
		capture.observer.gate = nil
	}
	events := slices.Clone(capture.observer.events[capture.start:])
	snapshot := WireSnapshot{Events: events}
	if capture.gate != nil {
		snapshot.MaximumActiveRanges = capture.gate.maximum.Load()
		snapshot.ActiveRanges = capture.gate.active.Load()
	}
	return snapshot
}

// RoundTrip records safe facts and wraps ranged response bodies for cleanup and
// concurrency evidence.
func (transport *observingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	observer := transport.observer
	observer.mu.Lock()
	phase := observer.phase
	gate := observer.gate
	observer.mu.Unlock()
	event := WireEvent{
		Phase: phase, Route: transport.route,
		Origin: originClass(request.URL, observer.registryHost),
		Method: request.Method, Endpoint: endpointClass(request.URL.Path),
		Range: request.Header.Get("Range"), RequestBytes: request.ContentLength,
		QueryKeys:        queryKeys(request.URL),
		SensitiveHeaders: hasSensitiveHeaders(request.Header),
	}
	var sourceFailed atomic.Bool
	if request.Body != nil && request.Body != http.NoBody {
		clone := request.Clone(request.Context())
		clone.Body = &observedRequestBody{body: request.Body, failed: &sourceFailed}
		request = clone
	}
	response, err := transport.next.RoundTrip(request)
	event.SourceBodyError = sourceFailed.Load()
	if err != nil {
		event.TransportError = true
		observer.appendEvent(event)
		return nil, err
	}
	event.Status = response.StatusCode
	event.ContentRange = response.Header.Get("Content-Range")
	event.ResponseRange = response.Header.Get("Range")
	event.LocationForm, event.LocationQueryKeys = locationFacts(
		response.Header.Get("Location"),
		request.URL,
		observer.registryHost,
	)
	observer.appendEvent(event)
	if gate != nil && event.Range != "" && response.StatusCode == http.StatusPartialContent {
		response.Body = &observedBody{body: response.Body, requestDone: request.Context().Done(), gate: gate}
	}
	return response, nil
}

// Read delegates to the request body and records any non-EOF source error.
func (body *observedRequestBody) Read(buffer []byte) (int, error) {
	count, err := body.body.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		body.failed.Store(true)
	}
	return count, err
}

// Close delegates to the original request body.
func (body *observedRequestBody) Close() error {
	return body.body.Close()
}

// appendEvent appends an event under the observer lock.
func (observer *wireObserver) appendEvent(event WireEvent) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.events = append(observer.events, event)
}

// Read joins the parallel gate once, then delegates to the response body.
func (body *observedBody) Read(buffer []byte) (int, error) {
	body.entered.Do(func() {
		active := body.gate.active.Add(1)
		updateMaximum(&body.gate.maximum, active)
		if active >= body.gate.target {
			body.gate.readyOnce.Do(func() { close(body.gate.ready) })
		}
		select {
		case <-body.gate.ready:
		case <-body.gate.stopped:
		case <-body.requestDone:
		}
	})
	count, err := body.body.Read(buffer)
	if errors.Is(err, io.EOF) {
		body.leave()
	}
	return count, err
}

// Close releases the active count even when the body was closed before EOF.
func (body *observedBody) Close() error {
	body.leave()
	return body.body.Close()
}

// leave decrements active at most once for a body that entered the gate.
func (body *observedBody) leave() {
	body.left.Do(func() {
		entered := false
		body.entered.Do(func() { entered = true })
		if !entered {
			body.gate.active.Add(-1)
		}
	})
}

// updateMaximum raises maximum to candidate without moving it backward.
func updateMaximum(maximum *atomic.Int64, candidate int64) {
	for current := maximum.Load(); candidate > current; current = maximum.Load() {
		if maximum.CompareAndSwap(current, candidate) {
			return
		}
	}
}

// endpointClass reduces paths to stable protocol categories.
func endpointClass(path string) string {
	switch {
	case strings.Contains(path, "/blobs/uploads/"):
		return endpointUpload
	case strings.Contains(path, "/blobs/"):
		return "blob"
	case strings.Contains(path, "/manifests/"):
		return "manifest"
	case path == "/v2/" || path == "/v2":
		return originRegistry
	default:
		return "other"
	}
}

// queryKeys returns sorted parameter names and discards every value.
func queryKeys(target *url.URL) []string {
	keys := make([]string, 0, len(target.Query()))
	for key := range target.Query() {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// locationFacts returns only origin shape and parameter names.
func locationFacts(header string, requestURL *url.URL, registryHost string) (string, []string) {
	if header == "" {
		return "", nil
	}
	parsed, err := url.Parse(header)
	if err != nil {
		return locationInvalid, nil
	}
	keys := queryKeys(parsed)
	if !parsed.IsAbs() {
		return locationRelative, keys
	}
	if originClass(parsed, registryHost) == "registry" {
		return locationSameOrigin, keys
	}
	if requestURL != nil && sameOrigin(parsed, requestURL) {
		return locationSameOrigin, keys
	}
	return originOffRegistry, keys
}

// originClass reports whether target belongs to the configured registry.
func originClass(target *url.URL, registryHost string) string {
	if canonicalAuthority(target.Host, target.Scheme) == registryHost {
		return originRegistry
	}
	return originOffRegistry
}

// sameOrigin compares scheme plus canonical authority.
func sameOrigin(left, right *url.URL) bool {
	return strings.EqualFold(left.Scheme, right.Scheme) &&
		canonicalAuthority(left.Host, left.Scheme) == canonicalAuthority(right.Host, right.Scheme)
}

// canonicalAuthority normalizes case and implicit HTTP ports.
func canonicalAuthority(host, scheme string) string {
	parsed, err := url.Parse(strings.ToLower(scheme) + "://" + host)
	if err != nil {
		return strings.ToLower(host)
	}
	hostname := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if port == "" {
		if strings.EqualFold(scheme, "https") {
			port = "443"
		} else if strings.EqualFold(scheme, "http") {
			port = "80"
		}
	}
	return hostname + ":" + port
}

// hasSensitiveHeaders checks presence without reading or retaining values.
func hasSensitiveHeaders(header http.Header) bool {
	for _, name := range sensitiveHeaderNames() {
		if header.Get(name) != "" {
			return true
		}
	}
	return false
}

// sensitiveHeaderNames returns the security-relevant headers without mutable globals.
func sensitiveHeaderNames() []string {
	return []string{"Authorization", "Proxy-Authorization", "Cookie", "Cookie2", "Referer"}
}
