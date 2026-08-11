package blob

import (
	"math"
	"net/http"
	"reflect"
	"sync"
)

// maxParallelPullWorkers bounds channel capacity and goroutine fan-out for a
// caller-controlled option before either can become unsafe to allocate.
const maxParallelPullWorkers = 1024

// Client transfers blobs to and from OCI registries. It is safe for
// concurrent use. Create one with [New].
type Client struct {
	// httpClient routes registry and off-origin storage requests through
	// separate transports and applies the client's redirect policy.
	httpClient *http.Client

	// plainHTTP selects http:// instead of https:// for registry URLs.
	plainHTTP bool

	// retry bounds how failed requests are re-attempted.
	retry RetryPolicy

	// chunkSize enables chunked upload when positive.
	chunkSize int64

	// pullWorkers enables parallel pull when positive.
	pullWorkers int
	// pullChunk is the ranged-fetch size for parallel pull.
	pullChunk int64
	// bufPool recycles chunk buffers across parallel pulls; nil when
	// parallel pull is off.
	bufPool *sync.Pool
}

// Option configures a Client built by [New].
type Option func(*options)

// options collects the settings applied by New before the Client is
// assembled.
type options struct {
	// transport is the caller-injected port for registry-origin I/O.
	transport http.RoundTripper
	// storageTransport handles requests outside the registry origin.
	storageTransport http.RoundTripper
	// plainHTTP selects http:// registry URLs.
	plainHTTP bool
	// retry bounds how failed requests are re-attempted.
	retry RetryPolicy
	// chunkSize enables chunked upload when positive.
	chunkSize int64
	// pullWorkers enables parallel pull when positive.
	pullWorkers int
	// pullChunk is the ranged-fetch size for parallel pull.
	pullChunk int64
}

// New builds a Client from the given options.
//
// With no options the Client uses [http.DefaultTransport], speaks
// https, and retries transient failures per [DefaultRetryPolicy].
// Authentication is the caller's job: inject a transport that
// attaches credentials with [WithTransport].
//
// Example:
//
//	client := blob.New(blob.WithTransport(authTransport))
//	ok, err := client.Exists(ctx, repo, dgst)
func New(opts ...Option) *Client {
	o := options{
		transport:        http.DefaultTransport,
		storageTransport: http.DefaultTransport,
		plainHTTP:        false,
		retry:            DefaultRetryPolicy(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	c := &Client{
		httpClient: &http.Client{
			Transport: &scopedTransport{
				registry: o.transport,
				storage:  o.storageTransport,
			},
			CheckRedirect: checkRedirect,
		},
		plainHTTP:   o.plainHTTP,
		retry:       o.retry,
		chunkSize:   o.chunkSize,
		pullWorkers: o.pullWorkers,
		pullChunk:   o.pullChunk,
	}
	if c.pullWorkers > 0 {
		c.bufPool = &sync.Pool{}
	}
	return c
}

// WithParallelPull switches Pull to fetch blobs with workers
// concurrent ranged requests of chunkSize bytes each. Values below
// one for either parameter are ignored and leave single-stream pull
// in place. Worker counts above 1,024 and configurations whose workers ×
// chunkSize memory bound cannot be represented by an int64 are likewise
// ignored.
//
// Pull's contract does not change: chunks are emitted in order
// through the same digest-verifying reader. Memory use is bounded by
// roughly workers × chunkSize — the library's one deliberate
// exception to never buffering — and the caller sets that bound with
// these two parameters. When the registry does not serve ranges,
// Pull quietly falls back to a single stream: the toggle states
// intent, not a requirement.
func WithParallelPull(workers int, chunkSize int64) Option {
	return func(o *options) {
		if workers > 0 && workers <= maxParallelPullWorkers && chunkSize > 0 &&
			int64(workers) <= math.MaxInt64/chunkSize {
			o.pullWorkers = workers
			o.pullChunk = chunkSize
		}
	}
}

// WithChunkedUpload switches Push from the default monolithic upload
// to chunked PATCH uploads of chunkSize bytes. Values below one are
// ignored and leave monolithic upload in place.
//
// Chunked upload is spec-optional and broken on major hosted
// registries (ECR discards chunks after the first and still reports
// success), because mainstream clients never exercise it. It is an
// explicit opt-in, never a fallback: leave it off unless you have
// verified your registry against it. The client checks the
// registry's Range acknowledgement after every chunk and abandons
// the upload rather than store a blob silently missing bytes.
func WithChunkedUpload(chunkSize int64) Option {
	return func(o *options) {
		if chunkSize > 0 {
			o.chunkSize = chunkSize
		}
	}
}

// WithTransport sets the [http.RoundTripper] used for requests to the
// registry origin. This is the seam where registry authentication is
// injected: pass an authenticated transport from a library such as
// oras-go or go-containerregistry. Off-origin redirects and absolute
// upload locations do not pass through this transport. A nil or typed-nil
// transport keeps [http.DefaultTransport].
func WithTransport(rt http.RoundTripper) Option {
	return func(o *options) {
		if !isNilValue(rt) {
			o.transport = rt
		}
	}
}

// WithStorageTransport sets the [http.RoundTripper] used for off-origin
// storage and CDN requests. It receives requests after registry credentials,
// cookies, proxy credentials, and referrer data have been removed; use it for
// storage-specific TLS, proxy, or authentication behavior. A nil or typed-nil
// transport keeps [http.DefaultTransport].
func WithStorageTransport(rt http.RoundTripper) Option {
	return func(o *options) {
		if !isNilValue(rt) {
			o.storageTransport = rt
		}
	}
}

// WithPlainHTTP selects plain http:// registry URLs instead of https.
// Meant for local registries served without TLS; leave it off for
// anything reachable from the internet.
func WithPlainHTTP(plain bool) Option {
	return func(o *options) {
		o.plainHTTP = plain
	}
}

// scheme returns the URL scheme the Client addresses registries with.
func (c *Client) scheme() string {
	if c.plainHTTP {
		return registrySchemeHTTP
	}
	return registrySchemeHTTPS
}

// isNilValue reports whether value is nil directly or through a nil-capable
// concrete value stored in an interface.
func isNilValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	kind := reflected.Kind()
	nilable := kind == reflect.Chan || kind == reflect.Func || kind == reflect.Interface ||
		kind == reflect.Map || kind == reflect.Pointer || kind == reflect.Slice
	return nilable && reflected.IsNil()
}
