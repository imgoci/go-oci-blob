package blob

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/opencontainers/go-digest"
)

// Registry URL schemes shared by request construction and origin matching.
const (
	registrySchemeHTTPS = "https"
	registrySchemeHTTP  = "http"
)

// Repository addresses a blob store: a registry host plus a repository
// name within it.
type Repository struct {
	// Host is the registry host with an optional port, such as
	// "registry.example.com" or "localhost:5000". It carries no scheme;
	// the Client decides between https and plain http.
	Host string

	// Name is the repository path within the registry, such as
	// "library/ubuntu". It must match the OCI distribution spec's
	// repository name grammar.
	Name string
}

// Validate reports whether the Repository can address a registry.
//
// The host must be non-empty and free of a scheme or path. The name
// must match the OCI distribution spec grammar: slash-separated
// components of lowercase alphanumerics joined by ".", "_", "__", or
// one or more "-".
func (r Repository) Validate() error {
	if err := validateHost(r.Host); err != nil {
		return fmt.Errorf("invalid repository host %q: %w", r.Host, err)
	}
	if !validName(r.Name) {
		return fmt.Errorf("invalid repository name %q", r.Name)
	}
	return nil
}

// validateTarget checks the repository/digest pair that every blob
// operation addresses, before anything reaches the wire.
func validateTarget(repo Repository, dgst digest.Digest) error {
	if err := repo.Validate(); err != nil {
		return err
	}
	if err := dgst.Validate(); err != nil {
		return fmt.Errorf("invalid digest %q: %w", dgst, err)
	}
	return nil
}

// validateHost rejects hosts that are empty or smuggle in URL parts
// beyond host and port.
func validateHost(host string) error {
	switch {
	case host == "":
		return errors.New("host is empty")
	case strings.Contains(host, "://"):
		return errors.New("host must not include a scheme")
	case strings.Contains(host, "/"):
		return errors.New("host must not include a path")
	case strings.ContainsAny(host, " \t"):
		return errors.New("host must not contain whitespace")
	}
	authority, err := parseRegistryAuthority(host)
	if err != nil {
		return err
	}
	if authority.port != "" {
		port, err := strconv.ParseUint(authority.port, 10, 16)
		if err != nil || port == 0 {
			return fmt.Errorf("port %q must be a number from 1 to 65535", authority.port)
		}
	}
	return nil
}

// registryAuthority is the host and optional port parsed from a Repository.
type registryAuthority struct {
	// host is the hostname or IP literal without IPv6 brackets.
	host string
	// port is the decimal port, or empty when none was specified.
	port string
}

// parseRegistryAuthority validates host as a URL authority and separates
// its hostname from its optional port.
func parseRegistryAuthority(host string) (registryAuthority, error) {
	parsed, err := url.Parse("https://" + host)
	if err != nil {
		return registryAuthority{}, fmt.Errorf("host and port are malformed: %w", err)
	}
	if parsed.User != nil {
		return registryAuthority{}, errors.New("host must not include user information")
	}
	if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return registryAuthority{}, errors.New("host must not include a path, query, or fragment")
	}
	hostname := parsed.Hostname()
	if hostname == "" {
		return registryAuthority{}, errors.New("hostname is empty")
	}
	if strings.HasSuffix(host, ":") {
		return registryAuthority{}, errors.New("port is empty")
	}
	return registryAuthority{host: hostname, port: parsed.Port()}, nil
}

// canonicalRegistryAuthority normalizes DNS case, equivalent IP spellings,
// and the scheme's default port for same-registry comparisons.
func canonicalRegistryAuthority(host, scheme string) (registryAuthority, error) {
	authority, err := parseRegistryAuthority(host)
	if err != nil {
		return registryAuthority{}, err
	}
	if addr, err := netip.ParseAddr(authority.host); err == nil {
		authority.host = addr.String()
	} else {
		authority.host = strings.TrimSuffix(strings.ToLower(authority.host), ".")
	}
	if authority.port != "" {
		port, err := strconv.ParseUint(authority.port, 10, 16)
		if err != nil || port == 0 {
			return registryAuthority{}, fmt.Errorf("port %q must be a number from 1 to 65535", authority.port)
		}
		authority.port = strconv.FormatUint(port, 10)
	}
	if (scheme == registrySchemeHTTPS && authority.port == "443") ||
		(scheme == registrySchemeHTTP && authority.port == "80") {
		authority.port = ""
	}
	return authority, nil
}

// validName reports whether name matches the OCI repository name
// grammar: components separated by "/", where each component is
// [a-z0-9]+((\.|_|__|-+)[a-z0-9]+)*.
func validName(name string) bool {
	if name == "" {
		return false
	}
	for component := range strings.SplitSeq(name, "/") {
		if !validNameComponent(component) {
			return false
		}
	}
	return true
}

// validNameComponent checks one slash-separated path component against
// the grammar. Implemented as a scan instead of a regexp so validation
// stays allocation-free.
func validNameComponent(component string) bool {
	i := 0
	for {
		start := i
		for i < len(component) && isLowerAlnum(component[i]) {
			i++
		}
		if i == start {
			return false
		}
		if i == len(component) {
			return true
		}
		switch component[i] {
		case '.':
			i++
		case '_':
			i++
			if i < len(component) && component[i] == '_' {
				i++
			}
		case '-':
			for i < len(component) && component[i] == '-' {
				i++
			}
		default:
			return false
		}
	}
}

// isLowerAlnum reports whether c is a lowercase ASCII letter or digit.
func isLowerAlnum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}
