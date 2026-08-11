package blob

import (
	"errors"
	"fmt"
	"strings"
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
	return nil
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
