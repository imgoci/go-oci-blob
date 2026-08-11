package blob

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRepositoryValidate(t *testing.T) {
	tests := []struct {
		name    string
		repo    Repository
		wantErr string
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
			name: "accepts every separator the grammar allows",
			repo: Repository{Host: "r.io", Name: "a.b/c_d/e__f/g-h/i--j/k0"},
		},
		{
			name:    "rejects an empty host",
			repo:    Repository{Host: "", Name: "ubuntu"},
			wantErr: "host is empty",
		},
		{
			name:    "rejects a host carrying a scheme",
			repo:    Repository{Host: "https://registry.example.com", Name: "ubuntu"},
			wantErr: "must not include a scheme",
		},
		{
			name:    "rejects a host carrying a path",
			repo:    Repository{Host: "registry.example.com/v2", Name: "ubuntu"},
			wantErr: "must not include a path",
		},
		{
			name:    "rejects a host containing whitespace",
			repo:    Repository{Host: "registry example.com", Name: "ubuntu"},
			wantErr: "whitespace",
		},
		{
			name:    "rejects an empty name",
			repo:    Repository{Host: "r.io", Name: ""},
			wantErr: "invalid repository name",
		},
		{
			name:    "rejects uppercase in the name",
			repo:    Repository{Host: "r.io", Name: "Ubuntu"},
			wantErr: "invalid repository name",
		},
		{
			name:    "rejects a leading slash",
			repo:    Repository{Host: "r.io", Name: "/ubuntu"},
			wantErr: "invalid repository name",
		},
		{
			name:    "rejects a trailing slash",
			repo:    Repository{Host: "r.io", Name: "ubuntu/"},
			wantErr: "invalid repository name",
		},
		{
			name:    "rejects an empty path component",
			repo:    Repository{Host: "r.io", Name: "library//ubuntu"},
			wantErr: "invalid repository name",
		},
		{
			name:    "rejects a component starting with a separator",
			repo:    Repository{Host: "r.io", Name: "-ubuntu"},
			wantErr: "invalid repository name",
		},
		{
			name:    "rejects a component ending with a separator",
			repo:    Repository{Host: "r.io", Name: "ubuntu-"},
			wantErr: "invalid repository name",
		},
		{
			name:    "rejects adjacent distinct separators",
			repo:    Repository{Host: "r.io", Name: "a-.b"},
			wantErr: "invalid repository name",
		},
		{
			name:    "rejects a triple underscore",
			repo:    Repository{Host: "r.io", Name: "a___b"},
			wantErr: "invalid repository name",
		},
		{
			name:    "rejects a double dot",
			repo:    Repository{Host: "r.io", Name: "a..b"},
			wantErr: "invalid repository name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.repo.Validate()

			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}
