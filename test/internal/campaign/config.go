package campaign

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/opencontainers/go-digest"

	blob "github.com/imgoci/go-oci-blob"
)

const (
	// ConfigSchemaVersion is the only accepted campaign configuration version.
	ConfigSchemaVersion = 1
	// ResultSchemaVersion is the current report and aggregate schema version.
	ResultSchemaVersion = 1
	// DefaultParallelWorkers is the standard parallel Pull worker count.
	DefaultParallelWorkers = 4
	// DefaultParallelChunkBytes is the standard parallel Pull chunk size.
	DefaultParallelChunkBytes = int64(256 << 10)
	// DefaultUploadChunkBytes is the standard chunked Push chunk size.
	DefaultUploadChunkBytes = int64(1 << 20)
)

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Config describes one fresh compatibility campaign.
type Config struct {
	// SchemaVersion identifies the configuration contract.
	SchemaVersion int `json:"schema_version"`
	// Registry identifies the tested registry deployment.
	Registry RegistryConfig `json:"registry"`
	// Run identifies this execution and its isolated repositories.
	Run RunConfig `json:"run"`
	// TLS configures the trusted registry connection.
	TLS TLSConfig `json:"tls"`
	// Auth points to caller-owned credential files.
	Auth AuthConfig `json:"auth"`
	// ORAS configures the independent command-line verifier.
	ORAS ORASConfig `json:"oras"`
	// Seed identifies the blob populated independently before the campaign.
	Seed SeedConfig `json:"seed"`
	// Parameters controls bounded transfer and campaign behavior.
	Parameters ParameterConfig `json:"parameters"`
}

// RegistryConfig identifies a concrete registry product and deployment.
type RegistryConfig struct {
	// ID is the stable machine-readable registry identifier.
	ID string `json:"id"`
	// Product is the display name used in reports.
	Product string `json:"product"`
	// Version is the tested product version or service identity.
	Version string `json:"version"`
	// Backend records the storage or hosted-service backend.
	Backend string `json:"backend"`
	// Host is the registry authority without a URL scheme.
	Host string `json:"host"`
}

// RunConfig identifies the immutable library and isolated campaign resources.
type RunConfig struct {
	// ID salts all fixtures and separates this execution from prior runs.
	ID string `json:"id"`
	// LibraryCommit is the exact full commit tested by the consumer module.
	LibraryCommit string `json:"library_commit"`
	// SourceRepository is the independently seeded source repository.
	SourceRepository string `json:"source_repository"`
	// DestinationRepository is distinct and empty before Mount.
	DestinationRepository string `json:"destination_repository"`
	// WorkDir holds disposable independent-verification files.
	WorkDir string `json:"work_dir"`
	// BlockedFeatures lists feature IDs that an independently recorded external
	// policy prevents this run from measuring.
	BlockedFeatures []string `json:"blocked_features,omitempty"`
}

// TLSConfig identifies additional trust material for the registry.
type TLSConfig struct {
	// CAFile is an optional PEM certificate-authority bundle.
	CAFile string `json:"ca_file,omitempty"`
}

// AuthConfig points to one registry credential without embedding its secret.
type AuthConfig struct {
	// Username is the Basic or token-service username.
	Username string `json:"username,omitempty"`
	// PasswordFile contains a password and must not be group- or world-readable.
	PasswordFile string `json:"password_file,omitempty"`
	// RefreshTokenFile contains an OAuth refresh or identity token.
	RefreshTokenFile string `json:"refresh_token_file,omitempty"`
	// AccessTokenFile contains a ready registry access token.
	AccessTokenFile string `json:"access_token_file,omitempty"`
	// RequireAnonymousDenial requires the anonymous preflight to return 401 or 403.
	RequireAnonymousDenial bool `json:"require_anonymous_denial"`
}

// ORASConfig configures the independent ORAS blob fetches.
type ORASConfig struct {
	// Binary is the ORAS executable path or command name.
	Binary string `json:"binary"`
	// RegistryConfigFile is an ORAS-compatible mode-0600 authentication file.
	RegistryConfigFile string `json:"registry_config_file"`
}

// SeedConfig identifies the ORAS-seeded source blob.
type SeedConfig struct {
	// File contains the exact independently uploaded bytes.
	File string `json:"file"`
	// Digest is the expected sha256 digest string.
	Digest string `json:"digest"`
}

// ParameterConfig controls fixed concurrency, chunk, and timeout bounds.
type ParameterConfig struct {
	// ParallelWorkers is the WithParallelPull worker count.
	ParallelWorkers int `json:"parallel_workers,omitempty"`
	// ParallelChunkBytes is the WithParallelPull chunk size.
	ParallelChunkBytes int64 `json:"parallel_chunk_bytes,omitempty"`
	// UploadChunkBytes is the WithChunkedUpload chunk size.
	UploadChunkBytes int64 `json:"upload_chunk_bytes,omitempty"`
	// OperationTimeout bounds each individual library or control operation.
	OperationTimeout string `json:"operation_timeout,omitempty"`
	// CampaignTimeout bounds the complete run.
	CampaignTimeout string `json:"campaign_timeout,omitempty"`
	// AbsenceSettleTime is the interval over which failed uploads must stay absent.
	AbsenceSettleTime string `json:"absence_settle_time,omitempty"`
}

// durations contains parsed runtime bounds that are not serialized.
type durations struct {
	// operation bounds one operation.
	operation time.Duration
	// campaign bounds the full execution.
	campaign time.Duration
	// absenceSettle bounds post-failure absence observation.
	absenceSettle time.Duration
}

// LoadConfig reads, normalizes, and validates a JSON campaign configuration.
func LoadConfig(path string) (Config, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return Config{}, fmt.Errorf("resolving campaign config path: %w", err)
	}
	path = absolutePath
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("reading campaign config: %w", err)
	}
	var cfg Config
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decoding campaign config: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Config{}, err
	}
	cfg.resolvePaths(filepath.Dir(path))
	cfg.applyDefaults()
	if _, err := cfg.parseDurations(); err != nil {
		return Config{}, err
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// ensureJSONEOF rejects a second JSON value after the configuration object.
func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return errors.New("campaign config contains more than one JSON value")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("reading campaign config trailer: %w", err)
	}
	return nil
}

// resolvePaths makes every file input relative to the configuration file.
func (cfg *Config) resolvePaths(base string) {
	cfg.Run.WorkDir = resolvePath(base, cfg.Run.WorkDir)
	cfg.TLS.CAFile = resolvePath(base, cfg.TLS.CAFile)
	cfg.Auth.PasswordFile = resolvePath(base, cfg.Auth.PasswordFile)
	cfg.Auth.RefreshTokenFile = resolvePath(base, cfg.Auth.RefreshTokenFile)
	cfg.Auth.AccessTokenFile = resolvePath(base, cfg.Auth.AccessTokenFile)
	cfg.ORAS.RegistryConfigFile = resolvePath(base, cfg.ORAS.RegistryConfigFile)
	cfg.Seed.File = resolvePath(base, cfg.Seed.File)
}

// resolvePath returns an absolute cleaned path while preserving an empty value.
func resolvePath(base, value string) string {
	if value == "" {
		return ""
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Join(base, value)
}

// applyDefaults supplies stable corpus-wide parameters.
func (cfg *Config) applyDefaults() {
	if cfg.Parameters.ParallelWorkers == 0 {
		cfg.Parameters.ParallelWorkers = DefaultParallelWorkers
	}
	if cfg.Parameters.ParallelChunkBytes == 0 {
		cfg.Parameters.ParallelChunkBytes = DefaultParallelChunkBytes
	}
	if cfg.Parameters.UploadChunkBytes == 0 {
		cfg.Parameters.UploadChunkBytes = DefaultUploadChunkBytes
	}
	if cfg.Parameters.OperationTimeout == "" {
		cfg.Parameters.OperationTimeout = "90s"
	}
	if cfg.Parameters.CampaignTimeout == "" {
		cfg.Parameters.CampaignTimeout = "12m"
	}
	if cfg.Parameters.AbsenceSettleTime == "" {
		cfg.Parameters.AbsenceSettleTime = "1s"
	}
	if cfg.ORAS.Binary == "" {
		cfg.ORAS.Binary = "oras"
	}
}

// parseDurations validates and returns all configured duration strings.
func (cfg *Config) parseDurations() (durations, error) {
	operation, err := time.ParseDuration(cfg.Parameters.OperationTimeout)
	if err != nil || operation <= 0 {
		return durations{}, errors.New("parameters.operation_timeout must be a positive duration")
	}
	campaign, err := time.ParseDuration(cfg.Parameters.CampaignTimeout)
	if err != nil || campaign <= 0 {
		return durations{}, errors.New("parameters.campaign_timeout must be a positive duration")
	}
	settle, err := time.ParseDuration(cfg.Parameters.AbsenceSettleTime)
	if err != nil || settle <= 0 {
		return durations{}, errors.New("parameters.absence_settle_time must be a positive duration")
	}
	if operation > campaign {
		return durations{}, errors.New("operation timeout cannot exceed campaign timeout")
	}
	return durations{operation: operation, campaign: campaign, absenceSettle: settle}, nil
}

// validate rejects incomplete, unsafe, or non-isolated configurations.
//
//nolint:gocognit // Keeping the closed configuration contract in one pass is clearer.
func (cfg *Config) validate() error {
	if cfg.SchemaVersion != ConfigSchemaVersion {
		return fmt.Errorf("schema_version must be %d", ConfigSchemaVersion)
	}
	for name, value := range map[string]string{
		"registry.id": cfg.Registry.ID, "registry.product": cfg.Registry.Product,
		"registry.version": cfg.Registry.Version, "registry.backend": cfg.Registry.Backend,
		"registry.host": cfg.Registry.Host, "run.id": cfg.Run.ID,
		"run.source_repository":      cfg.Run.SourceRepository,
		"run.destination_repository": cfg.Run.DestinationRepository,
		"run.work_dir":               cfg.Run.WorkDir, "seed.file": cfg.Seed.File,
		"seed.digest": cfg.Seed.Digest, "oras.registry_config_file": cfg.ORAS.RegistryConfigFile,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if strings.Contains(cfg.Registry.Host, "://") || strings.ContainsAny(cfg.Registry.Host, "/?#") {
		return errors.New("registry.host must be an authority without a scheme or path")
	}
	if cfg.Run.SourceRepository == cfg.Run.DestinationRepository {
		return errors.New("source and destination repositories must differ")
	}
	seenBlocked := make(map[string]struct{}, len(cfg.Run.BlockedFeatures))
	for _, id := range cfg.Run.BlockedFeatures {
		if id != FeatureMount {
			return fmt.Errorf("run.blocked_features contains unsupported feature %q", id)
		}
		if _, exists := seenBlocked[id]; exists {
			return fmt.Errorf("run.blocked_features repeats %q", id)
		}
		seenBlocked[id] = struct{}{}
	}
	for name, repository := range map[string]string{
		"run.source_repository":      cfg.Run.SourceRepository,
		"run.destination_repository": cfg.Run.DestinationRepository,
	} {
		if err := (blob.Repository{Host: cfg.Registry.Host, Name: repository}).Validate(); err != nil {
			return fmt.Errorf("%s is invalid: %w", name, err)
		}
	}
	if !commitPattern.MatchString(cfg.Run.LibraryCommit) {
		return errors.New("run.library_commit must be a full lowercase Git commit")
	}
	seedDigest, err := digest.Parse(cfg.Seed.Digest)
	if err != nil || seedDigest.Algorithm() != digest.SHA256 {
		return errors.New("seed.digest must be a valid sha256 digest")
	}
	if cfg.Parameters.ParallelWorkers < 2 {
		return errors.New("parameters.parallel_workers must be at least 2")
	}
	if cfg.Parameters.ParallelChunkBytes <= 0 || cfg.Parameters.UploadChunkBytes <= 0 {
		return errors.New("chunk sizes must be positive")
	}
	credentialFiles := 0
	for _, path := range []string{cfg.Auth.PasswordFile, cfg.Auth.RefreshTokenFile, cfg.Auth.AccessTokenFile} {
		if path != "" {
			credentialFiles++
		}
	}
	if credentialFiles != 1 {
		return errors.New("exactly one credential file is required")
	}
	return nil
}
