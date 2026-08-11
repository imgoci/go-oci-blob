package blob

import (
	"net/http"
	"sync"
)

// Client transfers blobs to and from OCI registries. It is safe for
// concurrent use. Create one with [New].
type Client struct {
	// httpClient executes registry requests and follows redirects to
	// blob storage (S3, CDN). Go's http.Client strips Authorization on
	// cross-host redirects; the design depends on that, so registry
	// credentials never reach a storage host.
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
	// transport is the caller-injected port for all registry I/O.
	transport http.RoundTripper
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
	o := options{transport: http.DefaultTransport, plainHTTP: false, retry: DefaultRetryPolicy()}
	for _, opt := range opts {
		opt(&o)
	}
	c := &Client{
		httpClient:  &http.Client{Transport: o.transport},
		plainHTTP:   o.plainHTTP,
		retry:       o.retry,
		chunkSize:   o.chunkSize,
		pullWorkers: o.pullWorkers,
		pullChunk:   o.pullChunk,
	}
	if c.pullWorkers > 0 {
		c.bufPool = &sync.Pool{New: func() any {
			buf := make([]byte, c.pullChunk)
			return &buf
		}}
	}
	return c
}

// WithParallelPull switches Pull to fetch blobs with workers
// concurrent ranged requests of chunkSize bytes each. Values below
// one for either parameter are ignored and leave single-stream pull
// in place.
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
		if workers > 0 && chunkSize > 0 {
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

// WithTransport sets the [http.RoundTripper] used for every registry
// request. This is the seam where authentication is injected: pass an
// authenticated transport from a library such as oras-go or
// go-containerregistry. A nil transport selects
// [http.DefaultTransport].
func WithTransport(rt http.RoundTripper) Option {
	return func(o *options) {
		if rt != nil {
			o.transport = rt
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
		return "http"
	}
	return "https"
}
