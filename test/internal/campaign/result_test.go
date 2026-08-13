package campaign

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFeatureDefinitionsPreserveThePublishedCorpus locks the machine-readable
// identifiers to the exact order of the compatibility matrix.
func TestFeatureDefinitionsPreserveThePublishedCorpus(t *testing.T) {
	expected := []string{
		FeatureHTTPSAuth,
		FeatureSmallBlob,
		FeatureExists,
		FeatureSerialPull,
		FeatureProgress,
		FeaturePullRange,
		FeatureParallelPull,
		FeatureRangeFallback,
		FeatureResume,
		FeatureUnreferenced,
		FeatureMonolithicPush,
		FeatureEmptyBlob,
		FeatureChunkedPush,
		FeatureWrongDigest,
		FeatureExactSize,
		FeatureMount,
		FeatureConcurrency,
		FeatureOffOrigin,
		FeatureLocation,
		FeatureThrottleRetry,
	}

	definitions := FeatureDefinitions()
	require.Len(t, definitions, len(expected))
	for index, id := range expected {
		assert.Equal(t, id, definitions[index].ID, "feature %d changed position", index)
		assert.NotEmpty(t, definitions[index].Name, "feature %s needs a display name", id)
	}

	definitions[0].ID = "mutated"
	assert.Equal(t, FeatureHTTPSAuth, FeatureDefinitions()[0].ID,
		"callers must receive an independent feature-definition slice")
}

// TestValidateReportAcceptsOnlyTheCompleteOrderedCorpus ensures a publishable
// report cannot omit, duplicate, reorder, or invent a compatibility row.
func TestValidateReportAcceptsOnlyTheCompleteOrderedCorpus(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Report)
		errorText string
	}{
		{
			name:      "wrong schema",
			mutate:    func(report *Report) { report.SchemaVersion++ },
			errorText: "report schema_version must be 1",
		},
		{
			name: "missing row",
			mutate: func(report *Report) {
				report.Features = report.Features[:len(report.Features)-1]
			},
			errorText: "features, want 20",
		},
		{
			name: "reordered rows",
			mutate: func(report *Report) {
				report.Features[0], report.Features[1] = report.Features[1], report.Features[0]
			},
			errorText: "feature 0",
		},
		{
			name: "changed display name",
			mutate: func(report *Report) {
				report.Features[0].Name = "TLS"
			},
			errorText: "feature 0",
		},
		{
			name: "invalid published status",
			mutate: func(report *Report) {
				report.Features[0].Status = Status("FAIL")
			},
			errorText: "invalid status",
		},
		{
			name: "PASS with failed assertion",
			mutate: func(report *Report) {
				report.Features[0].Assertions = []Assertion{{Name: "wire proof", OK: false}}
			},
			errorText: "PASS with failed assertion",
		},
	}

	require.NoError(t, validateReport(validReport()))
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := validReport()
			test.mutate(&report)

			err := validateReport(report)
			require.Error(t, err)
			assert.ErrorContains(t, err, test.errorText)
		})
	}
}

// TestValidateReportAllowsDiagnosticsForAnInvalidRun confirms a harness fault
// can be recorded without manufacturing a complete compatibility matrix.
func TestValidateReportAllowsDiagnosticsForAnInvalidRun(t *testing.T) {
	report := Report{
		SchemaVersion: ResultSchemaVersion,
		RunValid:      false,
		Diagnostics:   []string{"source seed control failed"},
	}

	assert.NoError(t, validateReport(report))
}

// TestWriteReportProtectsOutputAndRefusesCredentialValues exercises both the
// successful atomic publication path and its fail-closed secret scan.
func TestWriteReportProtectsOutputAndRefusesCredentialValues(t *testing.T) {
	t.Run("writes private valid JSON", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "evidence", "report.json")
		report := validReport()

		require.NoError(t, WriteReport(path, report, []string{"not-present-secret"}))

		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, byte('\n'), data[len(data)-1])
		var decoded Report
		require.NoError(t, json.Unmarshal(data, &decoded))
		assert.Equal(t, report.Features, decoded.Features)
	})

	t.Run("leaves existing report untouched when a secret is present", func(t *testing.T) {
		directory := t.TempDir()
		path := filepath.Join(directory, "report.json")
		original := []byte("previous valid evidence\n")
		writeTestFile(t, path, original)
		report := validReport()
		report.Features[0].Summary = "registry accepted credential super-secret-value"

		err := WriteReport(path, report, []string{"super-secret-value"})
		require.Error(t, err)
		require.ErrorContains(t, err, "credential value")
		contents, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		assert.Equal(t, original, contents)
	})

	t.Run("rejects a JSON-escaped secret", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "report.json")
		report := validReport()
		secret := `<token value="sensitive">`
		report.Features[0].Summary = "registry response contained " + secret

		err := WriteReport(path, report, []string{secret})
		require.Error(t, err)
		require.ErrorContains(t, err, "credential value")
		_, statErr := os.Stat(path)
		assert.ErrorIs(t, statErr, os.ErrNotExist)
	})
}

// TestSanitizeTextRemovesQueriesAndLineBreaks proves diagnostics retain useful
// endpoint context without signed upload state or multiline injection.
func TestSanitizeTextRemovesQueriesAndLineBreaks(t *testing.T) {
	input := "commit https://registry.example/v2/repo/uploads/id?state=first-secret\n" +
		"redirect https://cdn.example/blob?signature=second-secret failed " +
		"session /v2/private/blobs/uploads/opaque-capability#third-secret"

	got := sanitizeText(input)

	assert.Equal(t,
		"commit https://registry.example/v2/repo/uploads/id?REDACTED "+
			"redirect https://cdn.example/blob?REDACTED failed "+
			"session /v2/private/blobs/uploads/REDACTED",
		got,
	)
	assert.NotContains(t, got, "first-secret")
	assert.NotContains(t, got, "second-secret")
	assert.NotContains(t, got, "opaque-capability")
	assert.NotContains(t, got, "third-secret")
}

// TestFeatureResultUsesTheCanonicalDisplayName keeps probe implementations
// from duplicating user-visible row names.
func TestFeatureResultUsesTheCanonicalDisplayName(t *testing.T) {
	result := featureResult(
		FeatureUnreferenced,
		StatusPass,
		"observed",
		"raw retrieval matched",
		Assertion{Name: "exact bytes", OK: true, Observed: "4194304"},
	)

	assert.Equal(t, "Unreferenced blob retrieval", result.Name)
	assert.Equal(t, "observed", result.Qualifier)
	require.Len(t, result.Assertions, 1)
	assert.True(t, result.Assertions[0].OK)
}

// validReport returns a complete report with every feature passing.
func validReport() Report {
	definitions := FeatureDefinitions()
	features := make([]FeatureResult, 0, len(definitions))
	for _, definition := range definitions {
		features = append(features, featureResult(
			definition.ID,
			StatusPass,
			"",
			"independent verification passed",
		))
	}
	return Report{
		SchemaVersion: ResultSchemaVersion,
		Features:      slices.Clone(features),
		RunValid:      true,
	}
}
