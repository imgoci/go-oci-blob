package campaign

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadConfigAppliesDefaultsAndResolvesPaths proves that a compact campaign
// file expands into one unambiguous runtime configuration.
func TestLoadConfigAppliesDefaultsAndResolvesPaths(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "campaign.json")
	cfg := validConfig()
	cfg.TLS.CAFile = "tls/ca.pem"
	writeConfig(t, path, cfg)

	loaded, err := LoadConfig(path)
	require.NoError(t, err)

	assert.Equal(t, DefaultParallelWorkers, loaded.Parameters.ParallelWorkers)
	assert.Equal(t, DefaultParallelChunkBytes, loaded.Parameters.ParallelChunkBytes)
	assert.Equal(t, DefaultUploadChunkBytes, loaded.Parameters.UploadChunkBytes)
	assert.Equal(t, "90s", loaded.Parameters.OperationTimeout)
	assert.Equal(t, "12m", loaded.Parameters.CampaignTimeout)
	assert.Equal(t, "1s", loaded.Parameters.AbsenceSettleTime)
	assert.Equal(t, "oras", loaded.ORAS.Binary)
	assert.Equal(t, filepath.Join(directory, "work"), loaded.Run.WorkDir)
	assert.Equal(t, filepath.Join(directory, "tls", "ca.pem"), loaded.TLS.CAFile)
	assert.Equal(t, filepath.Join(directory, "password"), loaded.Auth.PasswordFile)
	assert.Equal(t, filepath.Join(directory, "oras-config.json"), loaded.ORAS.RegistryConfigFile)
	assert.Equal(t, filepath.Join(directory, "seed.bin"), loaded.Seed.File)

	bounds, err := loaded.parseDurations()
	require.NoError(t, err)
	assert.Equal(t, 90*time.Second, bounds.operation)
	assert.Equal(t, 12*time.Minute, bounds.campaign)
	assert.Equal(t, time.Second, bounds.absenceSettle)
}

// TestLoadConfigRejectsUnknownOrTrailingJSON ensures configuration typos and
// concatenated documents cannot be silently accepted.
func TestLoadConfigRejectsUnknownOrTrailingJSON(t *testing.T) {
	base, err := json.Marshal(validConfig())
	require.NoError(t, err)

	tests := []struct {
		name      string
		contents  []byte
		errorText string
	}{
		{
			name: "unknown field",
			contents: append(
				append([]byte(nil), base[:len(base)-1]...),
				[]byte(`,"unexpected":true}`)...,
			),
			errorText: "unknown field",
		},
		{
			name:      "second JSON value",
			contents:  append(append([]byte(nil), base...), []byte("\n{}\n")...),
			errorText: "more than one JSON value",
		},
		{
			name:      "non-JSON trailer",
			contents:  append(append([]byte(nil), base...), []byte("\ntrailer\n")...),
			errorText: "campaign config trailer",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "campaign.json")
			writeTestFile(t, path, test.contents)

			_, err := LoadConfig(path)
			require.Error(t, err)
			assert.ErrorContains(t, err, test.errorText)
		})
	}
}

// TestLoadConfigRejectsAmbiguousCampaignIdentity covers the validations that
// prevent the runner from targeting the wrong registry, source, or revision.
func TestLoadConfigRejectsAmbiguousCampaignIdentity(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Config)
		errorText string
	}{
		{
			name:      "wrong schema",
			mutate:    func(cfg *Config) { cfg.SchemaVersion++ },
			errorText: "schema_version must be 1",
		},
		{
			name:      "registry URL instead of authority",
			mutate:    func(cfg *Config) { cfg.Registry.Host = "https://registry.example.test" },
			errorText: "authority without a scheme or path",
		},
		{
			name:      "registry path",
			mutate:    func(cfg *Config) { cfg.Registry.Host = "registry.example.test/v2" },
			errorText: "authority without a scheme or path",
		},
		{
			name:      "shared source and destination",
			mutate:    func(cfg *Config) { cfg.Run.DestinationRepository = cfg.Run.SourceRepository },
			errorText: "source and destination repositories must differ",
		},
		{
			name:      "invalid source repository",
			mutate:    func(cfg *Config) { cfg.Run.SourceRepository = "Uppercase/source" },
			errorText: "run.source_repository is invalid",
		},
		{
			name:      "abbreviated commit",
			mutate:    func(cfg *Config) { cfg.Run.LibraryCommit = "0123456" },
			errorText: "full lowercase Git commit",
		},
		{
			name:      "uppercase commit",
			mutate:    func(cfg *Config) { cfg.Run.LibraryCommit = strings.Repeat("A", 40) },
			errorText: "full lowercase Git commit",
		},
		{
			name:      "blank required value",
			mutate:    func(cfg *Config) { cfg.Registry.Product = " \t" },
			errorText: "registry.product is required",
		},
		{
			name:      "non-sha256 seed digest",
			mutate:    func(cfg *Config) { cfg.Seed.Digest = "sha512:" + strings.Repeat("b", 128) },
			errorText: "seed.digest must be a valid sha256 digest",
		},
		{
			name:      "malformed seed digest",
			mutate:    func(cfg *Config) { cfg.Seed.Digest = "sha256:not-a-digest" },
			errorText: "seed.digest must be a valid sha256 digest",
		},
	}

	assertConfigErrors(t, tests)
}

// TestLoadConfigRejectsUnsafeRuntimeBounds covers the validations that prevent
// unbounded work and accidental unauthenticated runs.
func TestLoadConfigRejectsUnsafeRuntimeBounds(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Config)
		errorText string
	}{
		{
			name:      "one parallel worker",
			mutate:    func(cfg *Config) { cfg.Parameters.ParallelWorkers = 1 },
			errorText: "parallel_workers must be at least 2",
		},
		{
			name:      "negative parallel chunk",
			mutate:    func(cfg *Config) { cfg.Parameters.ParallelChunkBytes = -1 },
			errorText: "chunk sizes must be positive",
		},
		{
			name:      "negative upload chunk",
			mutate:    func(cfg *Config) { cfg.Parameters.UploadChunkBytes = -1 },
			errorText: "chunk sizes must be positive",
		},
		{
			name:      "invalid operation timeout",
			mutate:    func(cfg *Config) { cfg.Parameters.OperationTimeout = "soon" },
			errorText: "operation_timeout must be a positive duration",
		},
		{
			name: "operation exceeds campaign",
			mutate: func(cfg *Config) {
				cfg.Parameters.OperationTimeout = "2m"
				cfg.Parameters.CampaignTimeout = "1m"
			},
			errorText: "operation timeout cannot exceed campaign timeout",
		},
		{
			name: "missing credential",
			mutate: func(cfg *Config) {
				cfg.Auth.PasswordFile = ""
				cfg.Auth.RefreshTokenFile = ""
				cfg.Auth.AccessTokenFile = ""
			},
			errorText: "exactly one credential file is required",
		},
		{
			name: "multiple credentials",
			mutate: func(cfg *Config) {
				cfg.Auth.AccessTokenFile = "access-token"
			},
			errorText: "exactly one credential file is required",
		},
	}

	assertConfigErrors(t, tests)
}

// assertConfigErrors loads each mutated configuration and checks its failure.
func assertConfigErrors(t *testing.T, tests []struct {
	name      string
	mutate    func(*Config)
	errorText string
}) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := validConfig()
			test.mutate(&cfg)
			path := filepath.Join(t.TempDir(), "campaign.json")
			writeConfig(t, path, cfg)

			_, err := LoadConfig(path)
			require.Error(t, err)
			assert.ErrorContains(t, err, test.errorText)
		})
	}
}

// validConfig returns the smallest complete campaign configuration before
// defaults and path normalization are applied.
func validConfig() Config {
	return Config{
		SchemaVersion: ConfigSchemaVersion,
		Registry: RegistryConfig{
			ID:      "example",
			Product: "Example Registry",
			Version: "1.2.3",
			Backend: "filesystem",
			Host:    "registry.example.test:5443",
		},
		Run: RunConfig{
			ID:                    "campaign-20260812-001",
			LibraryCommit:         strings.Repeat("a", 40),
			SourceRepository:      "compat/source-campaign-20260812-001",
			DestinationRepository: "compat/destination-campaign-20260812-001",
			WorkDir:               "work",
		},
		Auth: AuthConfig{
			Username:     "agent",
			PasswordFile: "password",
		},
		ORAS: ORASConfig{
			RegistryConfigFile: "oras-config.json",
		},
		Seed: SeedConfig{
			File:   "seed.bin",
			Digest: "sha256:" + strings.Repeat("b", 64),
		},
	}
}

// writeConfig serializes cfg as a campaign file for LoadConfig tests.
func writeConfig(t *testing.T, path string, cfg Config) {
	t.Helper()
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	writeTestFile(t, path, data)
}

// writeTestFile creates one private test fixture.
func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, data, 0o600))
}
