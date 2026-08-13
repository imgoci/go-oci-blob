// Keep this module path outside github.com/imgoci/go-oci-blob so Go enforces
// the same internal-package boundary faced by an independent consumer.
module github.com/imgoci/go-oci-blob-compat

go 1.26.5

require (
	github.com/imgoci/go-oci-blob v0.0.0
	github.com/opencontainers/go-digest v1.0.0
	github.com/stretchr/testify v1.11.1
	oras.land/oras-go/v2 v2.6.2
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/imgoci/go-oci-blob => ..
