package campaign

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	blob "github.com/imgoci/go-oci-blob"
)

const (
	// runModeNormal identifies an ordinary executable.
	runModeNormal = "normal"
	// runModeRace identifies a race-enabled executable.
	runModeRace = "race"
)

// RunOptions carries command-line attestations that cannot be inferred safely.
type RunOptions struct {
	// ImmutableConsumer attests that the executable uses a read-only exported library tree.
	ImmutableConsumer bool
}

// campaignRunner owns live clients, controls, and fixtures for one fresh run.
type campaignRunner struct {
	// cfg is the validated normalized campaign configuration.
	cfg Config
	// durations contains parsed operation and campaign bounds.
	durations durations
	// bundle owns authenticated, storage, and observed transports.
	bundle *transportBundle
	// observer is shared by every public library client.
	observer *wireObserver
	// raw is the independent authenticated HTTP verifier.
	raw *http.Client
	// oras is the independent external-client verifier.
	oras orasVerifier
	// source is the independently seeded repository.
	source blob.Repository
	// destination begins empty for the Mount probe.
	destination blob.Repository
	// seed was uploaded independently before the runner started.
	seed fixture
	// serial is the default public library client.
	serial *blob.Client
	// parallel opts into concurrent ranged Pull.
	parallel *blob.Client
	// chunked opts into PATCH upload.
	chunked *blob.Client
}

// Run executes one complete normal or race compatibility campaign.
func Run(ctx context.Context, cfg Config, options RunOptions) (Report, []string, error) {
	report := invalidReport(cfg, options.ImmutableConsumer)
	if !options.ImmutableConsumer {
		return invalidateRun(report, nil, errors.New("immutable consumer attestation is required"))
	}
	bounds, err := cfg.parseDurations()
	if err != nil {
		return invalidateRun(report, nil, err)
	}
	campaignCtx, cancel := context.WithTimeout(ctx, bounds.campaign)
	defer cancel()
	material, err := loadCredential(cfg.Auth)
	if err != nil {
		return invalidateRun(report, nil, err)
	}
	if privacyErr := validatePrivateFile(cfg.ORAS.RegistryConfigFile); privacyErr != nil {
		return invalidateRun(
			report,
			material.secrets,
			fmt.Errorf("ORAS registry config is not private: %w", privacyErr),
		)
	}
	secrets := material.secrets
	bundle, err := newTransportBundle(cfg, material)
	if err != nil {
		return invalidateRun(report, secrets, err)
	}
	defer bundle.closeIdle()
	seed, err := loadSeed(cfg.Seed)
	if err != nil {
		return invalidateRun(report, secrets, err)
	}
	minimumSeedBytes := int64(cfg.Parameters.ParallelWorkers) * cfg.Parameters.ParallelChunkBytes
	if int64(len(seed.data)) <= minimumSeedBytes {
		return invalidateRun(report, secrets, fmt.Errorf(
			"seed must exceed %d bytes to exercise every parallel worker", minimumSeedBytes,
		))
	}
	report.Run.SeedDigest = seed.digest.String()
	report.Run.SeedBytes = int64(len(seed.data))
	runner := newCampaignRunner(cfg, bounds, bundle, seed)
	report, err = runner.run(withRepositoryScopes(campaignCtx, cfg), report)
	if err != nil {
		return invalidateRun(report, secrets, err)
	}
	return report, secrets, nil
}

// invalidateRun records a redacted diagnostic without manufacturing feature data.
func invalidateRun(report Report, secrets []string, err error) (Report, []string, error) {
	report.RunValid = false
	report.RaceClean = false
	report.Diagnostics = []string{"campaign invariant or infrastructure control failed"}
	return report, secrets, err
}

// newCampaignRunner constructs public API clients over one shared observer.
func newCampaignRunner(cfg Config, bounds durations, bundle *transportBundle, seed fixture) *campaignRunner {
	baseOptions := []blob.Option{
		blob.WithTransport(bundle.registry),
		blob.WithStorageTransport(bundle.storage),
	}
	return &campaignRunner{
		cfg: cfg, durations: bounds, bundle: bundle, observer: bundle.observer,
		raw: newControlClient(cfg, bundle),
		oras: orasVerifier{
			binary: cfg.ORAS.Binary, registryConfig: cfg.ORAS.RegistryConfigFile,
			caFile: cfg.TLS.CAFile, workDir: cfg.Run.WorkDir,
		},
		source:      blob.Repository{Host: cfg.Registry.Host, Name: cfg.Run.SourceRepository},
		destination: blob.Repository{Host: cfg.Registry.Host, Name: cfg.Run.DestinationRepository},
		seed:        seed,
		serial:      blob.New(baseOptions...),
		parallel: blob.New(append(baseOptions,
			blob.WithParallelPull(cfg.Parameters.ParallelWorkers, cfg.Parameters.ParallelChunkBytes),
		)...),
		chunked: blob.New(append(baseOptions, blob.WithChunkedUpload(cfg.Parameters.UploadChunkBytes))...),
	}
}

// run validates controls and executes the feature phases in stable order.
func (runner *campaignRunner) run(ctx context.Context, report Report) (Report, error) {
	var err error
	report, err = runner.verifyControls(ctx, report)
	if err != nil {
		return report, err
	}

	readCtx, cancel := runner.operationContext(ctx)
	reads, err := runner.probeReads(readCtx)
	cancel()
	if err != nil {
		return report, err
	}
	writeCtx, cancel := runner.operationContext(ctx)
	writes, err := runner.probeWrites(writeCtx)
	cancel()
	if err != nil {
		return report, err
	}
	mountCtx, cancel := runner.operationContext(ctx)
	mount, err := runner.probeMount(mountCtx)
	cancel()
	if err != nil {
		return report, err
	}
	report.Controls.DestinationAbsentBeforeMount = true
	concurrentCtx, cancel := runner.operationContext(ctx)
	concurrency, err := runner.probeConcurrency(concurrentCtx)
	cancel()
	if err != nil {
		return report, err
	}

	security, err := runner.securityResult()
	if err != nil {
		return report, err
	}
	location := runner.locationResult()
	throttle := runner.throttleResult()
	report.Features = []FeatureResult{
		featureResult(
			FeatureHTTPSAuth,
			StatusPass,
			"",
			"trusted HTTPS succeeded with maintained registry authentication; anonymous policy control passed",
		),
		writes.small,
		reads.exists,
		reads.serial,
		reads.progress,
		reads.pullRange,
		reads.parallel,
		reads.fallback,
		reads.resume,
		writes.unreferenced,
		writes.monolithic,
		writes.empty,
		writes.chunked,
		writes.wrongDigest,
		writes.exactSize,
		mount,
		concurrency,
		security,
		location,
		throttle,
	}
	report.RunValid = true
	report.RaceClean = raceClean()
	return report, validateReport(report)
}

// verifyControls proves tool identity, TLS/auth reachability, and the ORAS seed
// with independent raw and external-client checks.
func (runner *campaignRunner) verifyControls(ctx context.Context, report Report) (Report, error) {
	versionCtx, cancel := runner.operationContext(ctx)
	orasVersion, err := runner.oras.version(versionCtx)
	cancel()
	if err != nil {
		return report, err
	}
	report.Run.ORASVersion = orasVersion
	controlCtx, cancel := runner.operationContext(ctx)
	err = anonymousPreflight(controlCtx, runner.cfg, runner.bundle.anonymous)
	cancel()
	if err != nil {
		return report, err
	}
	controlCtx, cancel = runner.operationContext(ctx)
	err = verifyRawExact(controlCtx, runner.raw, runner.cfg, runner.source.Name, runner.seed)
	cancel()
	if err != nil {
		return report, fmt.Errorf("raw independent seed control: %w", err)
	}
	report.Controls.SeedRawVerified = true
	controlCtx, cancel = runner.operationContext(ctx)
	err = runner.oras.verifyBlob(
		controlCtx,
		runner.cfg.Registry.Host,
		runner.source.Name,
		runner.seed.digest,
		runner.seed.data,
	)
	cancel()
	if err != nil {
		return report, fmt.Errorf("ORAS independent seed control: %w", err)
	}
	report.Controls.SeedORASVerified = true
	return report, nil
}

// invalidReport creates metadata even when a precondition fails.
func invalidReport(cfg Config, immutable bool) Report {
	mode := runModeNormal
	if raceEnabled() {
		mode = runModeRace
	}
	harnessCommit := cfg.Run.LibraryCommit
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				harnessCommit = setting.Value
			}
		}
	}
	return Report{
		SchemaVersion: ResultSchemaVersion,
		CreatedAt:     time.Now().UTC(),
		Registry:      cfg.Registry,
		Run: RunMetadata{
			ID: cfg.Run.ID, Mode: mode, LibraryCommit: cfg.Run.LibraryCommit,
			HarnessCommit: harnessCommit, GoVersion: runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH,
			SourceRepository: cfg.Run.SourceRepository, DestinationRepository: cfg.Run.DestinationRepository,
			SeedDigest: cfg.Seed.Digest, BlockedFeatures: slices.Clone(cfg.Run.BlockedFeatures),
		},
		Parameters: cfg.Parameters,
		Controls:   ControlResults{ImmutableConsumer: immutable},
		RunValid:   false,
	}
}

// operationContext bounds one logical campaign phase.
func (runner *campaignRunner) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, runner.durations.operation)
}

// securityResult synthesizes the origin-routing observations across the run.
func (runner *campaignRunner) securityResult() (FeatureResult, error) {
	runner.observer.mu.Lock()
	events := append([]WireEvent(nil), runner.observer.events...)
	runner.observer.mu.Unlock()
	offOrigin := 0
	for _, event := range events {
		misrouted := event.Route == routeRegistry && event.Origin == originOffRegistry
		leaked := event.Route == routeStorage && event.Origin == originOffRegistry && event.SensitiveHeaders
		if misrouted || leaked || (event.Route == routeStorage && event.Origin == originRegistry) {
			return FeatureResult{}, errors.New("wire observer detected wrong-origin routing or sensitive headers")
		}
		if event.Route == routeStorage && event.Origin == originOffRegistry {
			offOrigin++
		}
	}
	if offOrigin == 0 {
		return featureResult(FeatureOffOrigin, StatusNotApplicable, "", "registry produced no off-origin request"), nil
	}
	return featureResult(
		FeatureOffOrigin,
		StatusPass,
		"observed",
		fmt.Sprintf("%d off-origin storage requests carried no sensitive headers", offOrigin),
	), nil
}

// locationResult summarizes upload Location forms seen on successful phases.
func (runner *campaignRunner) locationResult() FeatureResult {
	runner.observer.mu.Lock()
	events := append([]WireEvent(nil), runner.observer.events...)
	runner.observer.mu.Unlock()
	forms := map[string]int{}
	for _, event := range events {
		if event.LocationForm != "" && event.Status >= 200 && event.Status < 300 {
			forms[event.LocationForm]++
		}
	}
	if len(forms) == 0 {
		return featureResult(FeatureLocation, StatusNotApplicable, "", "no successful response carried a Location")
	}
	formsList := make([]string, 0, len(forms))
	for form, count := range forms {
		formsList = append(formsList, fmt.Sprintf("%s=%d", form, count))
	}
	slices.Sort(formsList)
	return featureResult(
		FeatureLocation,
		StatusPass,
		"observed",
		fmt.Sprintf("successful uploads followed redacted Location forms %s", strings.Join(formsList, ", ")),
	)
}

// throttleResult reports live retry only when the registry naturally emitted a
// retryable status and a later request in the same phase succeeded.
func (runner *campaignRunner) throttleResult() FeatureResult {
	runner.observer.mu.Lock()
	events := append([]WireEvent(nil), runner.observer.events...)
	runner.observer.mu.Unlock()
	for index, event := range events {
		if event.Phase == FeatureConcurrency || event.Method == http.MethodDelete {
			continue
		}
		if event.Status != http.StatusTooManyRequests && (event.Status < 500 || event.Status > 599) {
			continue
		}
		for _, later := range events[index+1:] {
			if later.Phase == event.Phase && later.Method == event.Method && later.Endpoint == event.Endpoint &&
				later.Range == event.Range &&
				later.Status >= 200 && later.Status < 300 {
				return featureResult(
					FeatureThrottleRetry,
					StatusPass,
					"observed",
					"registry emitted 429 or 5xx and a later same-operation attempt succeeded",
				)
			}
		}
	}
	return featureResult(
		FeatureThrottleRetry,
		StatusNotApplicable,
		"",
		"registry emitted no natural 429 or retryable 5xx",
	)
}
