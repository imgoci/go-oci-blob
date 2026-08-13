package campaign

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAggregateRequiresFreshNormalAndRaceRuns proves a publishable aggregate
// cannot reuse fixture state or omit either execution mode.
func TestAggregateRequiresFreshNormalAndRaceRuns(t *testing.T) {
	tests := []struct {
		name      string
		reports   func() []Report
		errorText string
	}{
		{
			name: "one report",
			reports: func() []Report {
				return []Report{freshReport("normal", "normal-1", "normal-1")}
			},
			errorText: "at least normal and race reports",
		},
		{
			name: "no race report",
			reports: func() []Report {
				return []Report{
					freshReport("normal", "normal-1", "normal-1"),
					freshReport("normal", "normal-2", "normal-2"),
				}
			},
			errorText: "at least one normal and one race report",
		},
		{
			name: "no normal report",
			reports: func() []Report {
				return []Report{
					freshReport("race", "race-1", "race-1"),
					freshReport("race", "race-2", "race-2"),
				}
			},
			errorText: "at least one normal and one race report",
		},
		{
			name: "reused run ID",
			reports: func() []Report {
				return []Report{
					freshReport("normal", "shared", "normal-1"),
					freshReport("race", "shared", "race-1"),
				}
			},
			errorText: "run ID \"shared\" was reused",
		},
		{
			name: "reused repository",
			reports: func() []Report {
				normal := freshReport("normal", "normal-1", "normal-1")
				race := freshReport("race", "race-1", "race-1")
				race.Run.SourceRepository = normal.Run.SourceRepository
				return []Report{normal, race}
			},
			errorText: "was reused across runs",
		},
		{
			name: "reused seed digest",
			reports: func() []Report {
				normal := freshReport("normal", "normal-1", "normal-1")
				race := freshReport("race", "race-1", "race-1")
				race.Run.SeedDigest = normal.Run.SeedDigest
				return []Report{normal, race}
			},
			errorText: "seed digest",
		},
		{
			name: "different seed size",
			reports: func() []Report {
				normal := freshReport("normal", "normal-1", "normal-1")
				race := freshReport("race", "race-1", "race-1")
				race.Run.SeedBytes++
				return []Report{normal, race}
			},
			errorText: "different seed size",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Aggregate(test.reports())
			require.Error(t, err)
			assert.ErrorContains(t, err, test.errorText)
		})
	}
}

// TestAggregateRejectsInvalidControlsAndRaceEvidence proves compatibility
// classifications cannot survive a broken independent control or dirty race run.
func TestAggregateRejectsInvalidControlsAndRaceEvidence(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Report)
		errorText string
	}{
		{
			name:      "invalid run",
			mutate:    func(report *Report) { report.RunValid = false },
			errorText: "run_valid is false",
		},
		{
			name:      "mutable consumer",
			mutate:    func(report *Report) { report.Controls.ImmutableConsumer = false },
			errorText: "independent controls did not pass",
		},
		{
			name:      "raw seed mismatch",
			mutate:    func(report *Report) { report.Controls.SeedRawVerified = false },
			errorText: "independent controls did not pass",
		},
		{
			name:      "ORAS seed mismatch",
			mutate:    func(report *Report) { report.Controls.SeedORASVerified = false },
			errorText: "independent controls did not pass",
		},
		{
			name: "dirty mount destination",
			mutate: func(report *Report) {
				report.Controls.DestinationAbsentBeforeMount = false
			},
			errorText: "independent controls did not pass",
		},
		{
			name:      "race detector not clean",
			mutate:    func(report *Report) { report.RaceClean = false },
			errorText: "race report is not race-clean",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			normal := freshReport("normal", "normal-1", "normal-1")
			race := freshReport("race", "race-1", "race-1")
			test.mutate(&race)

			_, err := Aggregate([]Report{normal, race})
			require.Error(t, err)
			assert.ErrorContains(t, err, test.errorText)
		})
	}
}

// TestAggregateAppliesConservativeFeatureRules locks the matrix policy for
// unsupported, observed, blocked, and opportunistic behavior.
func TestAggregateAppliesConservativeFeatureRules(t *testing.T) {
	t.Run("one NO requires a fresh reproduction", func(t *testing.T) {
		normal, race := freshReportPair()
		setFeature(t, &normal, FeatureEmptyBlob, StatusNo, "")

		_, err := Aggregate([]Report{normal, race})
		require.Error(t, err)
		assert.ErrorContains(t, err, "has one NO; reproduce it")
	})

	t.Run("two NO results win over a pass", func(t *testing.T) {
		normal, race := freshReportPair()
		repeat := freshReport("normal", "normal-2", "normal-2")
		setFeature(t, &normal, FeatureEmptyBlob, StatusNo, "")
		setFeature(t, &race, FeatureEmptyBlob, StatusNo, "")

		aggregate, err := Aggregate([]Report{normal, race, repeat})
		require.NoError(t, err)
		feature := requireAggregateFeature(t, aggregate, FeatureEmptyBlob)
		assert.Equal(t, StatusNo, feature.Status)
		assert.Equal(t, StatusCounts{Pass: 1, No: 2}, feature.Counts)
	})

	t.Run("PASS plus N/A remains PASS and preserves observed", func(t *testing.T) {
		normal, race := freshReportPair()
		setFeature(t, &normal, FeatureOffOrigin, StatusPass, "observed")
		setFeature(t, &race, FeatureOffOrigin, StatusNotApplicable, "")

		aggregate, err := Aggregate([]Report{normal, race})
		require.NoError(t, err)
		feature := requireAggregateFeature(t, aggregate, FeatureOffOrigin)
		assert.Equal(t, StatusPass, feature.Status)
		assert.Equal(t, "observed", feature.Qualifier)
		assert.Equal(t, StatusCounts{Pass: 1, NotApplicable: 1}, feature.Counts)
	})

	t.Run("all N/A remains N/A", func(t *testing.T) {
		normal, race := freshReportPair()
		setFeature(t, &normal, FeatureThrottleRetry, StatusNotApplicable, "")
		setFeature(t, &race, FeatureThrottleRetry, StatusNotApplicable, "")

		aggregate, err := Aggregate([]Report{normal, race})
		require.NoError(t, err)
		feature := requireAggregateFeature(t, aggregate, FeatureThrottleRetry)
		assert.Equal(t, StatusNotApplicable, feature.Status)
		assert.Equal(t, StatusCounts{NotApplicable: 2}, feature.Counts)
	})

	t.Run("blocked plus N/A remains BLOCKED", func(t *testing.T) {
		normal, race := freshReportPair()
		setFeature(t, &normal, FeatureThrottleRetry, StatusBlocked, "")
		setFeature(t, &race, FeatureThrottleRetry, StatusNotApplicable, "")

		aggregate, err := Aggregate([]Report{normal, race})
		require.NoError(t, err)
		feature := requireAggregateFeature(t, aggregate, FeatureThrottleRetry)
		assert.Equal(t, StatusBlocked, feature.Status)
		assert.Equal(t, StatusCounts{Blocked: 1, NotApplicable: 1}, feature.Counts)
	})
}

// freshReportPair returns distinct normal and race reports for one deployment.
func freshReportPair() (Report, Report) {
	return freshReport("normal", "normal-1", "normal-1"),
		freshReport("race", "race-1", "race-1")
}

// freshReport returns one complete independently controlled run.
func freshReport(mode, runID, repositorySuffix string) Report {
	report := validReport()
	report.Registry = RegistryConfig{
		ID: "example", Product: "Example Registry", Version: "1.2.3", Backend: "filesystem",
		Host: "registry.example.test",
	}
	report.Run = RunMetadata{
		ID: runID, Mode: mode,
		LibraryCommit: strings.Repeat("a", 40), HarnessCommit: strings.Repeat("b", 40),
		GoVersion: "go1.26.4 test/arch", ORASVersion: "1.3.0",
		SourceRepository:      "compat/source-" + repositorySuffix,
		DestinationRepository: "compat/destination-" + repositorySuffix,
		SeedDigest:            "sha256:" + fmt.Sprintf("%064x", sha256.Sum256([]byte(runID))),
		SeedBytes:             3_146_509,
	}
	report.Parameters = ParameterConfig{
		ParallelWorkers: 4, ParallelChunkBytes: 256 << 10, UploadChunkBytes: 1 << 20,
		OperationTimeout: "90s", CampaignTimeout: "12m", AbsenceSettleTime: "1s",
	}
	report.Controls = ControlResults{
		ImmutableConsumer: true, SeedRawVerified: true, SeedORASVerified: true,
		DestinationAbsentBeforeMount: true,
	}
	report.RaceClean = mode == "race"
	return report
}

// setFeature replaces one row's classification in report.
func setFeature(t *testing.T, report *Report, id string, status Status, qualifier string) {
	t.Helper()
	for index := range report.Features {
		if report.Features[index].ID == id {
			report.Features[index].Status = status
			report.Features[index].Qualifier = qualifier
			return
		}
	}
	require.Failf(t, "missing feature", "feature %s was not in the report", id)
}

// requireAggregateFeature returns one aggregate feature or fails the test.
func requireAggregateFeature(t *testing.T, aggregate AggregateReport, id string) AggregateFeature {
	t.Helper()
	for _, feature := range aggregate.Features {
		if feature.ID == id {
			return feature
		}
	}
	require.Failf(t, "missing feature", "feature %s was not in the aggregate", id)
	return AggregateFeature{}
}
