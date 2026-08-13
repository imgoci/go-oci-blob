package campaign

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Status is one published compatibility classification.
type Status string

const (
	// StatusPass means the behavior worked and independent verification passed.
	StatusPass Status = "PASS"
	// StatusNo means the registry and library combination did not support the behavior.
	StatusNo Status = "NO"
	// StatusBlocked means setup or policy prevented a valid measurement.
	StatusBlocked Status = "BLOCKED"
	// StatusNotApplicable means the live registry did not exercise an opportunistic path.
	StatusNotApplicable Status = "N/A"
)

const (
	// FeatureHTTPSAuth identifies TLS and authenticated registry access.
	FeatureHTTPSAuth = "https_authentication"
	// FeatureSmallBlob identifies the approximately one-KiB transfer probe.
	FeatureSmallBlob = "small_blob"
	// FeatureExists identifies present and missing Exists behavior.
	FeatureExists = "exists"
	// FeatureSerialPull identifies verified single-stream Pull.
	FeatureSerialPull = "serial_pull"
	// FeatureProgress identifies transfer progress reporting.
	FeatureProgress = "progress"
	// FeaturePullRange identifies exact partial reads.
	FeaturePullRange = "pull_range"
	// FeatureParallelPull identifies concurrent ranged Pull.
	FeatureParallelPull = "parallel_pull"
	// FeatureRangeFallback identifies quiet fallback when ranges are ignored.
	FeatureRangeFallback = "parallel_range_fallback"
	// FeatureResume identifies interrupted Pull continuation.
	FeatureResume = "interrupted_pull_resume"
	// FeatureUnreferenced identifies retrieval before manifest linking.
	FeatureUnreferenced = "unreferenced_blob_retrieval"
	// FeatureMonolithicPush identifies the default POST and final PUT upload.
	FeatureMonolithicPush = "monolithic_push"
	// FeatureEmptyBlob identifies empty Push and Pull behavior.
	FeatureEmptyBlob = "empty_blob"
	// FeatureChunkedPush identifies opt-in PATCH upload behavior.
	FeatureChunkedPush = "chunked_push"
	// FeatureWrongDigest identifies wrong-digest rejection and absence.
	FeatureWrongDigest = "wrong_digest_rejection"
	// FeatureExactSize identifies short and trailing reader rejection.
	FeatureExactSize = "exact_size_rejection"
	// FeatureMount identifies true cross-repository blob mounting.
	FeatureMount = "cross_repository_mount"
	// FeatureConcurrency identifies safe mixed use of one Client.
	FeatureConcurrency = "shared_client_concurrency"
	// FeatureOffOrigin identifies registry credential isolation.
	FeatureOffOrigin = "off_origin_credential_scope"
	// FeatureLocation identifies opaque upload Location handling.
	FeatureLocation = "upload_location_handling"
	// FeatureThrottleRetry identifies successful retry after live registry throttling.
	FeatureThrottleRetry = "registry_throttling_retry"
)

// FeatureDefinition supplies stable ordering and display names.
type FeatureDefinition struct {
	// ID is the stable machine-readable feature identifier.
	ID string `json:"id"`
	// Name is the human-readable matrix row.
	Name string `json:"name"`
}

// FeatureDefinitions is the ordered 20-row compatibility corpus.
func FeatureDefinitions() []FeatureDefinition {
	return []FeatureDefinition{
		{ID: FeatureHTTPSAuth, Name: "HTTPS and authentication"},
		{ID: FeatureSmallBlob, Name: "Small blob, about 1 KiB"},
		{ID: FeatureExists, Name: "Exists, present and missing"},
		{ID: FeatureSerialPull, Name: "Serial Pull"},
		{ID: FeatureProgress, Name: "Progress reporting"},
		{ID: FeaturePullRange, Name: "PullRange"},
		{ID: FeatureParallelPull, Name: "Parallel Pull"},
		{ID: FeatureRangeFallback, Name: "Parallel range-ignored fallback"},
		{ID: FeatureResume, Name: "Interrupted Pull resume"},
		{ID: FeatureUnreferenced, Name: "Unreferenced blob retrieval"},
		{ID: FeatureMonolithicPush, Name: "Monolithic Push"},
		{ID: FeatureEmptyBlob, Name: "Empty blob Push and Pull"},
		{ID: FeatureChunkedPush, Name: "Chunked Push"},
		{ID: FeatureWrongDigest, Name: "Wrong-digest rejection"},
		{ID: FeatureExactSize, Name: "Exact-size rejection"},
		{ID: FeatureMount, Name: "Cross-repository Mount"},
		{ID: FeatureConcurrency, Name: "Shared-client concurrency"},
		{ID: FeatureOffOrigin, Name: "Off-origin redirect credential scope"},
		{ID: FeatureLocation, Name: "Upload Location handling"},
		{ID: FeatureThrottleRetry, Name: "Retry after registry throttling"},
	}
}

// Report is one normal or race-enabled campaign result.
type Report struct {
	// SchemaVersion identifies the report contract.
	SchemaVersion int `json:"schema_version"`
	// CreatedAt is the UTC report creation time.
	CreatedAt time.Time `json:"created_at"`
	// Registry identifies the tested deployment.
	Registry RegistryConfig `json:"registry"`
	// Run records immutable execution metadata.
	Run RunMetadata `json:"run"`
	// Parameters records the transfer configuration.
	Parameters ParameterConfig `json:"parameters"`
	// Controls records the independent preconditions.
	Controls ControlResults `json:"controls"`
	// Features contains the ordered compatibility classifications.
	Features []FeatureResult `json:"features"`
	// RunValid is false when a harness or infrastructure invariant failed.
	RunValid bool `json:"run_valid"`
	// RaceClean is true only for a completed race-enabled campaign.
	RaceClean bool `json:"race_clean"`
	// Diagnostics contains redacted invalid-run explanations.
	Diagnostics []string `json:"diagnostics,omitempty"`
}

// RunMetadata records one exact compatibility executable invocation.
type RunMetadata struct {
	// ID is the unique fixture salt and execution identifier.
	ID string `json:"id"`
	// Mode is normal or race and is derived from the compiled binary.
	Mode string `json:"mode"`
	// LibraryCommit is the exact tested library revision.
	LibraryCommit string `json:"library_commit"`
	// HarnessCommit is the compatibility executable's VCS revision when available.
	HarnessCommit string `json:"harness_commit,omitempty"`
	// GoVersion identifies the runtime toolchain and platform.
	GoVersion string `json:"go_version"`
	// ORASVersion identifies the independent verifier.
	ORASVersion string `json:"oras_version"`
	// SourceRepository is the independently seeded repository.
	SourceRepository string `json:"source_repository"`
	// DestinationRepository is the initially empty Mount target.
	DestinationRepository string `json:"destination_repository"`
	// SeedDigest is the unique independently seeded blob identity.
	SeedDigest string `json:"seed_digest"`
	// SeedBytes is the exact independently seeded blob size.
	SeedBytes int64 `json:"seed_bytes"`
	// BlockedFeatures records operator-attested external policy blocks.
	BlockedFeatures []string `json:"blocked_features,omitempty"`
}

// ControlResults records prerequisites that distinguish compatibility data from a broken run.
type ControlResults struct {
	// ImmutableConsumer is true when the caller attested to an immutable exported source tree.
	ImmutableConsumer bool `json:"immutable_consumer"`
	// SeedRawVerified means authenticated raw HTTP matched the ORAS-seeded bytes.
	SeedRawVerified bool `json:"seed_raw_verified"`
	// SeedORASVerified means an independent ORAS fetch matched the seed fixture.
	SeedORASVerified bool `json:"seed_oras_verified"`
	// DestinationAbsentBeforeMount means HEAD and GET both proved absence.
	DestinationAbsentBeforeMount bool `json:"destination_absent_before_mount"`
}

// FeatureResult records one published matrix cell and its safe evidence summary.
type FeatureResult struct {
	// ID is the stable feature identifier.
	ID string `json:"id"`
	// Name is the display name.
	Name string `json:"name"`
	// Status is PASS, NO, BLOCKED, or N/A.
	Status Status `json:"status"`
	// Qualifier carries labels such as observed without changing status semantics.
	Qualifier string `json:"qualifier,omitempty"`
	// Summary is concise redacted operator evidence.
	Summary string `json:"summary"`
	// Assertions contains safe structured proof points.
	Assertions []Assertion `json:"assertions,omitempty"`
}

// Assertion records one non-secret proof point.
type Assertion struct {
	// Name identifies the checked invariant.
	Name string `json:"name"`
	// OK records whether the invariant held.
	OK bool `json:"ok"`
	// Observed is a short safe value such as a count or status.
	Observed string `json:"observed,omitempty"`
}

// featureResult constructs one ordered, validated feature row.
func featureResult(id string, status Status, qualifier, summary string, assertions ...Assertion) FeatureResult {
	name := ""
	for _, definition := range FeatureDefinitions() {
		if definition.ID == id {
			name = definition.Name
			break
		}
	}
	return FeatureResult{
		ID: id, Name: name, Status: status, Qualifier: qualifier,
		Summary: summary, Assertions: assertions,
	}
}

// validateReport confirms that a valid run contains exactly the stable corpus.
func validateReport(report Report) error {
	if report.SchemaVersion != ResultSchemaVersion {
		return fmt.Errorf("report schema_version must be %d", ResultSchemaVersion)
	}
	if !report.RunValid {
		return nil
	}
	definitions := FeatureDefinitions()
	if len(report.Features) != len(definitions) {
		return fmt.Errorf("valid report has %d features, want %d", len(report.Features), len(definitions))
	}
	for index, definition := range definitions {
		feature := report.Features[index]
		if feature.ID != definition.ID || feature.Name != definition.Name {
			return fmt.Errorf("feature %d is %q, want %q", index, feature.ID, definition.ID)
		}
		if !slices.Contains([]Status{StatusPass, StatusNo, StatusBlocked, StatusNotApplicable}, feature.Status) {
			return fmt.Errorf("feature %s has invalid status %q", feature.ID, feature.Status)
		}
		if feature.Status == StatusPass {
			for _, assertion := range feature.Assertions {
				if !assertion.OK {
					return fmt.Errorf("feature %s is PASS with failed assertion %q", feature.ID, assertion.Name)
				}
			}
		}
	}
	return nil
}

// WriteReport atomically writes redacted JSON and refuses known secret values.
func WriteReport(path string, report Report, secrets []string) error {
	if err := validateReport(report); err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding report: %w", err)
	}
	data = append(data, '\n')
	return writePrivateJSON(path, data, secrets)
}

// writePrivateJSON scans for credential values and atomically publishes data.
func writePrivateJSON(path string, data []byte, secrets []string) error {
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		encoded, marshalErr := json.Marshal(secret)
		if marshalErr != nil {
			return fmt.Errorf("encoding secret sentinel: %w", marshalErr)
		}
		encoded = bytes.TrimSuffix(bytes.TrimPrefix(encoded, []byte{'"'}), []byte{'"'})
		if bytes.Contains(data, []byte(secret)) || bytes.Contains(data, encoded) {
			return errors.New("refusing to write report containing a credential value")
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating report directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".registry-compat-*.json")
	if err != nil {
		return fmt.Errorf("creating report: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		return errors.Join(fmt.Errorf("protecting report: %w", err), temporary.Close())
	}
	if _, err := temporary.Write(data); err != nil {
		return errors.Join(fmt.Errorf("writing report: %w", err), temporary.Close())
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("closing report: %w", err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("publishing report: %w", err)
	}
	return nil
}

// sanitizeText removes URL query values, fragments, upload-session IDs, and
// line breaks from externally supplied text.
func sanitizeText(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = redactUploadSessions(value)
	value = redactDelimitedValue(value, '?')
	value = redactDelimitedValue(value, '#')
	return strings.TrimSpace(value)
}

// redactUploadSessions removes opaque capability path segments while retaining
// the stable Distribution endpoint category.
func redactUploadSessions(value string) string {
	const prefix = "/blobs/uploads/"
	searchFrom := 0
	for searchFrom < len(value) {
		relative := strings.Index(value[searchFrom:], prefix)
		if relative < 0 {
			break
		}
		start := searchFrom + relative + len(prefix)
		end := textValueEnd(value, start)
		if start == end {
			searchFrom = start
			continue
		}
		const marker = "REDACTED"
		value = value[:start] + marker + value[end:]
		searchFrom = start + len(marker)
	}
	return value
}

// redactDelimitedValue removes a query or fragment value wherever it appears
// in a URL-like token.
func redactDelimitedValue(value string, delimiter byte) string {
	searchFrom := 0
	for searchFrom < len(value) {
		relative := strings.IndexByte(value[searchFrom:], delimiter)
		if relative < 0 {
			break
		}
		position := searchFrom + relative
		end := textValueEnd(value, position+1)
		const marker = "REDACTED"
		value = value[:position+1] + marker + value[end:]
		searchFrom = position + 1 + len(marker)
	}
	return value
}

// textValueEnd finds the end of one unquoted URL-like token.
func textValueEnd(value string, start int) int {
	for index := start; index < len(value); index++ {
		if strings.ContainsRune(" \t\"'<>)]}", rune(value[index])) {
			return index
		}
	}
	return len(value)
}
