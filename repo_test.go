package blob

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRepositoryValidate(t *testing.T) {
	tests := []struct {
		name      string
		repo      Repository
		wantError bool
	}{
		{
			name: "accepts a plain host and simple name",
			repo: Repository{Host: "registry.example.com", Name: "ubuntu"},
		},
		{
			name: "accepts a host with port and a nested name",
			repo: Repository{Host: "localhost:5000", Name: "library/ubuntu"},
		},
		{
			name: "accepts a bracketed IPv6 host with port",
			repo: Repository{Host: "[2001:db8::1]:5000", Name: "library/ubuntu"},
		},
		{
			name: "accepts every separator the grammar allows",
			repo: Repository{Host: "r.io", Name: "a.b/c_d/e__f/g-h/i--j/k0"},
		},
		{
			name:      "rejects an empty host",
			repo:      Repository{Host: "", Name: "ubuntu"},
			wantError: true,
		},
		{
			name:      "rejects a host carrying a scheme",
			repo:      Repository{Host: "https://registry.example.com", Name: "ubuntu"},
			wantError: true,
		},
		{
			name:      "rejects a host carrying a path",
			repo:      Repository{Host: "registry.example.com/v2", Name: "ubuntu"},
			wantError: true,
		},
		{
			name:      "rejects a host containing whitespace",
			repo:      Repository{Host: "registry example.com", Name: "ubuntu"},
			wantError: true,
		},
		{
			name:      "rejects a nonnumeric port",
			repo:      Repository{Host: "registry.example.com:https", Name: "ubuntu"},
			wantError: true,
		},
		{
			name:      "rejects a port above 65535",
			repo:      Repository{Host: "registry.example.com:65536", Name: "ubuntu"},
			wantError: true,
		},
		{
			name:      "rejects an empty port",
			repo:      Repository{Host: "registry.example.com:", Name: "ubuntu"},
			wantError: true,
		},
		{
			name:      "rejects user information",
			repo:      Repository{Host: "user@registry.example.com", Name: "ubuntu"},
			wantError: true,
		},
		{
			name:      "rejects a host query",
			repo:      Repository{Host: "registry.example.com?debug=1", Name: "ubuntu"},
			wantError: true,
		},
		{
			name:      "rejects an empty name",
			repo:      Repository{Host: "r.io", Name: ""},
			wantError: true,
		},
		{
			name:      "rejects uppercase in the name",
			repo:      Repository{Host: "r.io", Name: "Ubuntu"},
			wantError: true,
		},
		{
			name:      "rejects a leading slash",
			repo:      Repository{Host: "r.io", Name: "/ubuntu"},
			wantError: true,
		},
		{
			name:      "rejects a trailing slash",
			repo:      Repository{Host: "r.io", Name: "ubuntu/"},
			wantError: true,
		},
		{
			name:      "rejects an empty path component",
			repo:      Repository{Host: "r.io", Name: "library//ubuntu"},
			wantError: true,
		},
		{
			name:      "rejects a component starting with a separator",
			repo:      Repository{Host: "r.io", Name: "-ubuntu"},
			wantError: true,
		},
		{
			name:      "rejects a component ending with a separator",
			repo:      Repository{Host: "r.io", Name: "ubuntu-"},
			wantError: true,
		},
		{
			name:      "rejects adjacent distinct separators",
			repo:      Repository{Host: "r.io", Name: "a-.b"},
			wantError: true,
		},
		{
			name:      "rejects a triple underscore",
			repo:      Repository{Host: "r.io", Name: "a___b"},
			wantError: true,
		},
		{
			name:      "rejects a double dot",
			repo:      Repository{Host: "r.io", Name: "a..b"},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.repo.Validate()

			if tt.wantError {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}
