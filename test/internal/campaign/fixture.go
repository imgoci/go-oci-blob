package campaign

import (
	"crypto/sha256"
	"fmt"

	"github.com/opencontainers/go-digest"
)

// fixture derives unique deterministic bytes from a run ID and probe label.
type fixture struct {
	// label identifies the probe that owns the bytes.
	label string
	// data is the exact transfer body.
	data []byte
	// digest is the canonical sha256 identity of data.
	digest digest.Digest
}

// newFixture returns size salted bytes without relying on random global state.
func newFixture(runID, label string, size int) fixture {
	data := make([]byte, size)
	for offset, counter := 0, 0; offset < len(data); counter++ {
		block := sha256.Sum256(fmt.Appendf(nil, "%s:%s:%d", runID, label, counter))
		offset += copy(data[offset:], block[:])
	}
	return fixture{label: label, data: data, digest: digest.FromBytes(data)}
}

// loadSeed reads the independently populated fixture and verifies its digest.
func loadSeed(cfg SeedConfig) (fixture, error) {
	data, err := readFixtureFile(cfg.File)
	if err != nil {
		return fixture{}, err
	}
	expected, err := digest.Parse(cfg.Digest)
	if err != nil {
		return fixture{}, fmt.Errorf("parsing seed digest: %w", err)
	}
	actual := digest.FromBytes(data)
	if actual != expected {
		return fixture{}, fmt.Errorf("seed digest is %s, configured %s", actual, expected)
	}
	return fixture{label: "independent-seed", data: data, digest: actual}, nil
}
