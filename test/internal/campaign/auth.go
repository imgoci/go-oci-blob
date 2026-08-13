package campaign

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"oras.land/oras-go/v2/registry/remote/auth"
)

const (
	// credentialPassword identifies a password credential source.
	credentialPassword = "password"
	// credentialRefreshToken identifies an OAuth refresh token source.
	credentialRefreshToken = "refresh token"
	// credentialAccessToken identifies a registry access token source.
	credentialAccessToken = "access token"
)

// credentialMaterial contains the live credential and the values that must
// never appear in retained evidence.
type credentialMaterial struct {
	// credential is passed to the maintained ORAS authentication client.
	credential auth.Credential
	// secrets contains every non-empty credential value.
	secrets []string
}

// authRoundTripper adapts the ORAS authentication client to [http.RoundTripper].
type authRoundTripper struct {
	// client performs one request without following redirects.
	client *auth.Client
}

// RoundTrip authenticates one registry request and leaves redirects to the
// go-oci-blob client, which owns origin routing.
func (rt *authRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return rt.client.Do(request)
}

// transportBundle owns the registry and storage transports for one campaign.
type transportBundle struct {
	// registry authenticates registry-origin requests.
	registry http.RoundTripper
	// storage handles credential-free off-origin requests.
	storage http.RoundTripper
	// controlRegistry authenticates independent raw registry controls without
	// contributing to library wire evidence.
	controlRegistry http.RoundTripper
	// controlStorage handles independent raw off-origin controls.
	controlStorage http.RoundTripper
	// anonymous makes unauthenticated control requests.
	anonymous *http.Client
	// observer records safe wire facts for both routes.
	observer *wireObserver
	// closeIdle releases caller-owned HTTP idle connections.
	closeIdle func()
}

// loadCredential reads exactly one protected credential source.
func loadCredential(cfg AuthConfig) (credentialMaterial, error) {
	credential := auth.Credential{Username: cfg.Username}
	var values []string
	for name, path := range map[string]string{
		credentialPassword: cfg.PasswordFile, credentialRefreshToken: cfg.RefreshTokenFile,
		credentialAccessToken: cfg.AccessTokenFile,
	} {
		if path == "" {
			continue
		}
		value, err := readSecretFile(path)
		if err != nil {
			return credentialMaterial{}, fmt.Errorf("reading %s: %w", name, err)
		}
		values = append(values, value)
		switch name {
		case credentialPassword:
			credential.Password = value
		case credentialRefreshToken:
			credential.RefreshToken = value
		case credentialAccessToken:
			credential.AccessToken = value
		}
	}
	if len(values) != 1 {
		return credentialMaterial{}, errors.New("exactly one credential file is required")
	}
	if credential.Password != "" {
		values = append(values, base64.StdEncoding.EncodeToString([]byte(credential.Username+":"+credential.Password)))
	}
	return credentialMaterial{credential: credential, secrets: values}, nil
}

// readSecretFile reads a regular mode-0600-or-stricter credential file.
func readSecretFile(path string) (string, error) {
	if err := validatePrivateFile(path); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value := strings.TrimRight(string(data), "\r\n")
	if value == "" {
		return "", errors.New("credential file is empty")
	}
	return value, nil
}

// validatePrivateFile requires a regular mode-0600-or-stricter input.
func validatePrivateFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("credential path is not a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("credential file permissions %04o allow group or other access", info.Mode().Perm())
	}
	return nil
}

// newTransportBundle constructs trusted, observed, and origin-separated
// transports without implementing registry authentication in the harness.
func newTransportBundle(cfg Config, material credentialMaterial) (*transportBundle, error) {
	registryBase, err := newBaseTransport(cfg.TLS.CAFile, cfg.Parameters.ParallelWorkers)
	if err != nil {
		return nil, err
	}
	storageBase := registryBase.Clone()
	observer := newWireObserver(cfg.Registry.Host, cfg.Parameters.ParallelWorkers)
	storageObserved := observer.wrap(routeStorage, storageBase)
	innerClient := &http.Client{
		Transport: registryBase,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	authClient := &auth.Client{
		Client:     innerClient,
		Credential: auth.StaticCredential(cfg.Registry.Host, material.credential),
		Cache:      auth.NewCache(),
		Header:     http.Header{"User-Agent": {"go-oci-blob-registry-compat/1"}},
	}
	registryAuthenticated := &authRoundTripper{client: authClient}
	controlAuthClient := *authClient
	controlAuthClient.Cache = auth.NewCache()
	return &transportBundle{
		registry:        observer.wrap(routeRegistry, registryAuthenticated),
		storage:         storageObserved,
		controlRegistry: &authRoundTripper{client: &controlAuthClient},
		controlStorage:  storageBase.Clone(),
		anonymous: &http.Client{
			Transport: registryBase.Clone(),
			Timeout:   20 * time.Second,
		},
		observer: observer,
		closeIdle: func() {
			registryBase.CloseIdleConnections()
			storageBase.CloseIdleConnections()
		},
	}, nil
}

// newBaseTransport clones the standard transport, adds optional trust, and
// sizes its pools for the configured parallel transfer.
func newBaseTransport(caFile string, workers int) (*http.Transport, error) {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("http.DefaultTransport is not an *http.Transport")
	}
	transport := base.Clone()
	transport.MaxIdleConns = max(transport.MaxIdleConns, workers*2)
	transport.MaxIdleConnsPerHost = max(transport.MaxIdleConnsPerHost, workers)
	transport.ResponseHeaderTimeout = 30 * time.Second
	if caFile == "" {
		return transport, nil
	}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("reading TLS CA: %w", err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("loading system certificate pool: %w", err)
	}
	if !roots.AppendCertsFromPEM(pem) {
		return nil, errors.New("TLS CA file contains no parseable certificate")
	}
	transport.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
	}
	return transport, nil
}

// withRepositoryScopes hints all source and destination permissions to the
// authentication client before a multi-operation campaign.
func withRepositoryScopes(ctx context.Context, cfg Config) context.Context {
	scopes := []string{
		auth.ScopeRepository(cfg.Run.SourceRepository, "pull", "push"),
		auth.ScopeRepository(cfg.Run.DestinationRepository, "pull", "push"),
	}
	return auth.WithScopesForHost(ctx, cfg.Registry.Host, scopes...)
}
