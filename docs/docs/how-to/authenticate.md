# How to authenticate to a registry

Inject an authenticated `http.RoundTripper` with `blob.WithTransport`. The
library never handles credentials itself; the transport you supply answers
the registry's authentication challenges.

## Prerequisites

- A credential the registry accepts: a username and password, personal access
  token, or a provider-issued token (for example, from
  `aws ecr get-login-password`).
- One of the credential-handling libraries below, or your own
  `http.RoundTripper`.

## Use an oras-go auth client

This is the pattern the library's nine-registry compatibility campaigns used
against Amazon ECR, GHCR, Docker Hub, `gcr.io`, Quay.io, Azure Container
Registry, Harbor, GitLab, and Nexus.

Add the dependency:

```sh
go get oras.land/oras-go/v2
```

Adapt `auth.Client` to `http.RoundTripper` and pass it to the blob client:

```go
package main

import (
	"net/http"

	blob "github.com/imgoci/go-oci-blob"
	"oras.land/oras-go/v2/registry/remote/auth"
)

// authTransport adapts an ORAS auth.Client to http.RoundTripper.
type authTransport struct {
	client *auth.Client
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.client.Do(req)
}

func newAuthenticatedClient(host, username, password string) *blob.Client {
	// The inner client must not follow redirects: the blob client owns
	// redirect handling and strips registry credentials from off-origin
	// requests. A redirect-following auth client would bypass that.
	inner := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	authClient := &auth.Client{
		Client: inner,
		Cache:  auth.NewCache(),
		Credential: auth.StaticCredential(host, auth.Credential{
			Username: username,
			Password: password,
		}),
	}
	return blob.New(blob.WithTransport(&authTransport{client: authClient}))
}
```

For a token credential (GHCR personal access token, `gcloud` access token,
the password half of `aws ecr get-login-password`), put it in the `Password`
field with the username the provider documents for token logins.

If one client performs many operations against the same repository, hint the
scopes once so the auth client requests a single token instead of one per
challenge:

```go
ctx = auth.WithScopesForHost(ctx, host,
	auth.ScopeRepository("myorg/myrepo", "pull", "push"))
```

## Use a go-containerregistry transport

`go-containerregistry` builds an authenticated `http.RoundTripper` directly.

Add the dependency:

```sh
go get github.com/google/go-containerregistry
```

Build the transport and pass it to the blob client:

```go
package main

import (
	"context"
	"net/http"

	blob "github.com/imgoci/go-oci-blob"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

func newAuthenticatedClient(ctx context.Context, repository string) (*blob.Client, error) {
	repo, err := name.NewRepository(repository) // "ghcr.io/myorg/myrepo"
	if err != nil {
		return nil, err
	}
	// DefaultKeychain reads the same credential store `docker login` writes.
	authenticator, err := authn.DefaultKeychain.Resolve(repo.Registry)
	if err != nil {
		return nil, err
	}
	rt, err := transport.NewWithContext(ctx, repo.Registry, authenticator,
		http.DefaultTransport, []string{repo.Scope(transport.PushScope)})
	if err != nil {
		return nil, err
	}
	return blob.New(blob.WithTransport(rt)), nil
}
```

## Verify

Run an authenticated call against a repository the credential can read:

```go
ok, err := client.Exists(ctx, blob.Repository{Host: host, Name: "myorg/myrepo"},
	digest.FromBytes(nil))
```

Any `(bool, nil)` result proves the credential worked: the registry answered
an authenticated request. An authentication failure surfaces as an error
wrapping the registry's `401` or `403` response.

## Off-origin storage requests

The blob client routes off-origin requests around your registry transport and
removes `Authorization`, `Proxy-Authorization`, cookies, and `Referer` before
they leave the registry origin. Set `blob.WithStorageTransport` when the
storage side needs its own TLS, proxy, authentication, or destination policy.
The library does not decide whether private or local addresses are safe for
your application; enforce those restrictions in the storage transport.

## Troubleshooting

### The registry keeps answering `401`

The credential is wrong or lacks scope for the repository. Test the same
credential with another client (for example `oras blob fetch` or
`docker login` plus a pull) to separate a credential problem from a code
problem.

### Pushes fail with `401` while pulls work

The token was issued with pull-only scope. Request `pull,push` scope, or with
the oras-go client, hint it via `auth.WithScopesForHost` as shown above.

## Related

- [Push and pull your first blob](../tutorials/getting-started.md) — no-auth
  local setup.
- [Design](../explanation/design.md) — why authentication is the caller's
  job and how origin scoping works.
