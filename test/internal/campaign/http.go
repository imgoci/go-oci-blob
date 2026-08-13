package campaign

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/opencontainers/go-digest"
)

// registryScheme keeps live campaigns on authenticated TLS endpoints.
const registryScheme = "https"

// scopedControlTransport applies the same origin split to independent raw
// checks while leaving authentication to the maintained registry transport.
type scopedControlTransport struct {
	// registryHost is the normalized registry authority.
	registryHost string
	// registry handles registry-origin requests.
	registry http.RoundTripper
	// storage handles off-origin requests.
	storage http.RoundTripper
}

// RoundTrip routes the request and strips sensitive metadata off-origin.
func (transport *scopedControlTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if originClass(request.URL, transport.registryHost) == originRegistry {
		return transport.registry.RoundTrip(request)
	}
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	for _, name := range sensitiveHeaderNames() {
		clone.Header.Del(name)
	}
	return transport.storage.RoundTrip(clone)
}

// newControlClient creates a bounded redirect-following independent HTTP client.
func newControlClient(cfg Config, bundle *transportBundle) *http.Client {
	return &http.Client{
		Transport: &scopedControlTransport{
			registryHost: canonicalAuthority(cfg.Registry.Host, "https"),
			registry:     bundle.controlRegistry,
			storage:      bundle.controlStorage,
		},
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("raw control followed too many redirects")
			}
			if len(via) > 0 && !sameOrigin(request.URL, via[len(via)-1].URL) {
				for _, name := range sensitiveHeaderNames() {
					request.Header.Del(name)
				}
			}
			return nil
		},
	}
}

// blobEndpoint constructs an escaped digest URL for independent controls.
func blobEndpoint(cfg Config, repository string, dgst digest.Digest) string {
	return (&url.URL{
		Scheme: registryScheme,
		Host:   cfg.Registry.Host,
		Path:   "/v2/" + repository + "/blobs/" + dgst.String(),
	}).String()
}

// rawBlob performs one independent blob request and returns status and bounded bytes.
func rawBlob(
	ctx context.Context,
	client *http.Client,
	cfg Config,
	method, repository string,
	dgst digest.Digest,
	limit int64,
) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, blobEndpoint(cfg, repository, dgst), nil)
	if err != nil {
		return 0, nil, fmt.Errorf("building raw blob request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, errors.New("raw blob request failed")
	}
	defer response.Body.Close()
	if method == http.MethodHead {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return response.StatusCode, nil, nil
	}
	if limit < 0 {
		limit = 0
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return response.StatusCode, nil, fmt.Errorf("reading raw blob response: %w", err)
	}
	if int64(len(body)) > limit {
		return response.StatusCode, nil, fmt.Errorf("raw blob response exceeded %d-byte limit", limit)
	}
	return response.StatusCode, body, nil
}

// rawManifestPut independently publishes one bounded manifest and returns only
// its status and bounded response bytes for causal classification.
func rawManifestPut(
	ctx context.Context,
	client *http.Client,
	cfg Config,
	repository, reference string,
	manifest []byte,
) (int, []byte, error) {
	target := (&url.URL{
		Scheme: registryScheme,
		Host:   cfg.Registry.Host,
		Path:   "/v2/" + repository + "/manifests/" + reference,
	}).String()
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, target, bytes.NewReader(manifest))
	if err != nil {
		return 0, nil, fmt.Errorf("building raw manifest request: %w", err)
	}
	request.Header.Set("Content-Type", imageManifestMediaType)
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, errors.New("raw manifest request failed")
	}
	defer response.Body.Close()
	const responseLimit = int64(64 << 10)
	body, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil {
		return response.StatusCode, nil, fmt.Errorf("reading raw manifest response: %w", err)
	}
	if int64(len(body)) > responseLimit {
		return response.StatusCode, nil, errors.New("raw manifest response exceeded limit")
	}
	return response.StatusCode, body, nil
}

// verifyRawExact requires a successful GET with exact bytes and digest.
func verifyRawExact(
	ctx context.Context,
	client *http.Client,
	cfg Config,
	repository string,
	value fixture,
) error {
	status, body, err := rawBlob(ctx, client, cfg, http.MethodGet, repository, value.digest, int64(len(value.data)))
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("raw GET returned %d, want 200", status)
	}
	if !bytes.Equal(body, value.data) {
		return fmt.Errorf("raw GET bytes differed for %s", value.label)
	}
	if digest.FromBytes(body) != value.digest {
		return fmt.Errorf("raw GET digest differed for %s", value.label)
	}
	return nil
}

// proveAbsent requires both HEAD and GET to report missing.
func proveAbsent(
	ctx context.Context,
	client *http.Client,
	cfg Config,
	repository string,
	dgst digest.Digest,
) error {
	for _, method := range []string{http.MethodHead, http.MethodGet} {
		status, _, err := rawBlob(ctx, client, cfg, method, repository, dgst, 64<<10)
		if err != nil {
			return err
		}
		if status != http.StatusNotFound {
			return fmt.Errorf("raw %s returned %d, want 404", method, status)
		}
	}
	return nil
}

// anonymousPreflight checks that the registry is reachable and optionally protected.
func anonymousPreflight(ctx context.Context, cfg Config, client *http.Client) error {
	target := (&url.URL{Scheme: registryScheme, Host: cfg.Registry.Host, Path: "/v2/"}).String()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("building anonymous preflight: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("anonymous preflight failed: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if cfg.Auth.RequireAnonymousDenial && response.StatusCode != http.StatusUnauthorized &&
		response.StatusCode != http.StatusForbidden {
		return fmt.Errorf("anonymous preflight returned %d, want 401 or 403", response.StatusCode)
	}
	if !cfg.Auth.RequireAnonymousDenial && response.StatusCode >= http.StatusInternalServerError {
		return fmt.Errorf("anonymous preflight returned %d", response.StatusCode)
	}
	return nil
}

// safeStatusSummary formats only method, endpoint category, and status.
func safeStatusSummary(events []WireEvent) string {
	parts := make([]string, 0, len(events))
	for _, event := range events {
		parts = append(parts, fmt.Sprintf("%s %s=%d", event.Method, event.Endpoint, event.Status))
	}
	return strings.Join(parts, ", ")
}
