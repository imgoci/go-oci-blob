package campaign

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"time"
)

// AggregateReport combines fresh normal and race campaign evidence.
type AggregateReport struct {
	// SchemaVersion identifies the aggregate contract.
	SchemaVersion int `json:"schema_version"`
	// CreatedAt is the UTC aggregation time.
	CreatedAt time.Time `json:"created_at"`
	// Registry identifies the tested deployment.
	Registry RegistryConfig `json:"registry"`
	// LibraryCommit is the exact tested library revision.
	LibraryCommit string `json:"library_commit"`
	// HarnessCommit is the shared harness revision when recorded.
	HarnessCommit string `json:"harness_commit,omitempty"`
	// Parameters records the common campaign configuration.
	Parameters ParameterConfig `json:"parameters"`
	// Runs records the number of valid reports considered.
	Runs int `json:"runs"`
	// NormalRuns records ordinary executable reports.
	NormalRuns int `json:"normal_runs"`
	// RaceRuns records race-enabled executable reports.
	RaceRuns int `json:"race_runs"`
	// Features contains the conservative ordered matrix.
	Features []AggregateFeature `json:"features"`
}

// AggregateFeature combines one feature across every valid fresh run.
type AggregateFeature struct {
	// ID is the stable feature identifier.
	ID string `json:"id"`
	// Name is the published display name.
	Name string `json:"name"`
	// Status is the conservatively aggregated result.
	Status Status `json:"status"`
	// Qualifier preserves observational evidence labels.
	Qualifier string `json:"qualifier,omitempty"`
	// Counts records the number of reports in each status.
	Counts StatusCounts `json:"counts"`
	// Summary describes the aggregation rule that selected Status.
	Summary string `json:"summary"`
}

// StatusCounts counts per-run feature classifications.
type StatusCounts struct {
	// Pass is the number of successful exercised runs.
	Pass int `json:"pass"`
	// No is the number of independently unsupported runs.
	No int `json:"no"`
	// Blocked is the number of runs prevented by setup or policy.
	Blocked int `json:"blocked"`
	// NotApplicable is the number of unexercised opportunistic paths.
	NotApplicable int `json:"not_applicable"`
}

// AggregateFiles reads reports from disk and applies conservative rules.
func AggregateFiles(paths []string) (AggregateReport, error) {
	if len(paths) < 2 {
		return AggregateReport{}, errors.New("aggregation needs at least normal and race reports")
	}
	reports := make([]Report, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return AggregateReport{}, fmt.Errorf("reading report %s: %w", path, err)
		}
		var report Report
		if err := json.Unmarshal(data, &report); err != nil {
			return AggregateReport{}, fmt.Errorf("decoding report %s: %w", path, err)
		}
		reports = append(reports, report)
	}
	return Aggregate(reports)
}

// Aggregate validates fresh normal and race runs and combines their rows.
func Aggregate(reports []Report) (AggregateReport, error) {
	if len(reports) < 2 {
		return AggregateReport{}, errors.New("aggregation needs at least normal and race reports")
	}
	first := reports[0]
	aggregate := AggregateReport{
		SchemaVersion: ResultSchemaVersion,
		CreatedAt:     time.Now().UTC(),
		Registry:      first.Registry,
		LibraryCommit: first.Run.LibraryCommit,
		HarnessCommit: first.Run.HarnessCommit,
		Parameters:    first.Parameters,
		Runs:          len(reports),
	}
	var err error
	aggregate.NormalRuns, aggregate.RaceRuns, err = validateAggregateSet(reports, first)
	if err != nil {
		return AggregateReport{}, err
	}
	if aggregate.NormalRuns == 0 || aggregate.RaceRuns == 0 {
		return AggregateReport{}, errors.New("aggregation requires at least one normal and one race report")
	}
	for featureIndex, definition := range FeatureDefinitions() {
		results := make([]FeatureResult, 0, len(reports))
		for _, report := range reports {
			results = append(results, report.Features[featureIndex])
		}
		feature, err := aggregateFeature(definition, results)
		if err != nil {
			return AggregateReport{}, err
		}
		aggregate.Features = append(aggregate.Features, feature)
	}
	return aggregate, nil
}

// validateAggregateSet proves a homogeneous collection of fresh normal and
// race reports and returns its mode counts.
//
//nolint:gocognit // The single pass keeps cross-report identity checks auditable.
func validateAggregateSet(reports []Report, first Report) (int, int, error) {
	seenRuns := make(map[string]struct{}, len(reports))
	seenRepositories := make(map[string]struct{}, len(reports)*2)
	seenSeeds := make(map[string]struct{}, len(reports))
	normalRuns := 0
	raceRuns := 0
	for index, report := range reports {
		if err := validateAggregateInput(report); err != nil {
			return 0, 0, fmt.Errorf("report %d is not publishable: %w", index, err)
		}
		if report.Registry != first.Registry || report.Run.LibraryCommit != first.Run.LibraryCommit ||
			report.Run.HarnessCommit != first.Run.HarnessCommit || !reflect.DeepEqual(report.Parameters, first.Parameters) {
			return 0, 0, fmt.Errorf("report %d targets different registry, revisions, or parameters", index)
		}
		if _, exists := seenRuns[report.Run.ID]; exists {
			return 0, 0, fmt.Errorf("run ID %q was reused", report.Run.ID)
		}
		seenRuns[report.Run.ID] = struct{}{}
		if report.Run.SeedDigest == "" || report.Run.SeedBytes <= 0 {
			return 0, 0, fmt.Errorf("report %d has no valid seed identity", index)
		}
		if _, exists := seenSeeds[report.Run.SeedDigest]; exists {
			return 0, 0, fmt.Errorf("seed digest %q was reused across runs", report.Run.SeedDigest)
		}
		seenSeeds[report.Run.SeedDigest] = struct{}{}
		if report.Run.SeedBytes != first.Run.SeedBytes ||
			!slices.Equal(report.Run.BlockedFeatures, first.Run.BlockedFeatures) {
			return 0, 0, fmt.Errorf("report %d used a different seed size or blocked-feature policy", index)
		}
		for _, repository := range []string{report.Run.SourceRepository, report.Run.DestinationRepository} {
			if _, exists := seenRepositories[repository]; exists {
				return 0, 0, fmt.Errorf("repository %q was reused across runs", repository)
			}
			seenRepositories[repository] = struct{}{}
		}
		switch report.Run.Mode {
		case runModeNormal:
			normalRuns++
		case runModeRace:
			raceRuns++
		default:
			return 0, 0, fmt.Errorf("report %d has unknown mode %q", index, report.Run.Mode)
		}
	}
	return normalRuns, raceRuns, nil
}

// validateAggregateInput rejects invalid controls and race reports with no
// clean race-detector result.
func validateAggregateInput(report Report) error {
	if err := validateReport(report); err != nil {
		return err
	}
	if !report.RunValid {
		return errors.New("run_valid is false")
	}
	if !report.Controls.ImmutableConsumer || !report.Controls.SeedRawVerified ||
		!report.Controls.SeedORASVerified || !report.Controls.DestinationAbsentBeforeMount {
		return errors.New("one or more independent controls did not pass")
	}
	if report.Run.Mode == runModeRace && !report.RaceClean {
		return errors.New("race report is not race-clean")
	}
	return nil
}

// aggregateFeature applies the corpus policy to one stable feature row.
func aggregateFeature(definition FeatureDefinition, results []FeatureResult) (AggregateFeature, error) {
	feature := AggregateFeature{ID: definition.ID, Name: definition.Name}
	observed := false
	for _, result := range results {
		switch result.Status {
		case StatusPass:
			feature.Counts.Pass++
			observed = observed || result.Qualifier == "observed"
		case StatusNo:
			feature.Counts.No++
		case StatusBlocked:
			feature.Counts.Blocked++
		case StatusNotApplicable:
			feature.Counts.NotApplicable++
		default:
			return AggregateFeature{}, fmt.Errorf("feature %s has invalid status %q", definition.ID, result.Status)
		}
	}
	switch {
	case feature.Counts.No == 1:
		return AggregateFeature{}, fmt.Errorf(
			"feature %s has one NO; reproduce it with a fresh run before aggregation",
			definition.ID,
		)
	case feature.Counts.No >= 2:
		feature.Status = StatusNo
		feature.Summary = fmt.Sprintf("unsupported in %d of %d fresh runs", feature.Counts.No, len(results))
	case feature.Counts.Pass > 0:
		feature.Status = StatusPass
		feature.Summary = fmt.Sprintf("passed in every one of %d exercised runs", feature.Counts.Pass)
		if observed {
			feature.Qualifier = "observed"
		}
	case feature.Counts.Blocked > 0:
		feature.Status = StatusBlocked
		feature.Summary = "all attempted measurements were blocked or not applicable"
	default:
		feature.Status = StatusNotApplicable
		feature.Summary = "no valid run naturally exercised this opportunistic path"
	}
	return feature, nil
}

// WriteAggregate writes one private aggregate report using the report writer's
// secret scanner and atomic-publication behavior.
func WriteAggregate(path string, aggregate AggregateReport, secrets []string) error {
	if aggregate.SchemaVersion != ResultSchemaVersion || len(aggregate.Features) != len(FeatureDefinitions()) {
		return errors.New("aggregate is incomplete")
	}
	data, err := json.MarshalIndent(aggregate, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding aggregate: %w", err)
	}
	return writePrivateJSON(path, append(data, '\n'), secrets)
}
