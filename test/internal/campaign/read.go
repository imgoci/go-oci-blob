package campaign

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/opencontainers/go-digest"

	blob "github.com/imgoci/go-oci-blob"
)

// readProbeResults groups the stable rows produced from the independent seed.
type readProbeResults struct {
	// exists is the present-and-missing Exists result.
	exists FeatureResult
	// serial is the serial Pull result.
	serial FeatureResult
	// progress is the progress callback result.
	progress FeatureResult
	// pullRange is the partial-range result.
	pullRange FeatureResult
	// parallel is the parallel-or-fallback Pull result.
	parallel FeatureResult
	// fallback records whether the registry ignored ranges.
	fallback FeatureResult
	// resume is the injected interrupted-body result.
	resume FeatureResult
}

// probeReads exercises all read paths against bytes seeded by ORAS.
// The function stays linear so each observer phase is visibly paired with its
// finish call and cannot accidentally include another probe's requests.
//
//nolint:funlen // The fixed seven-probe orchestration is clearer as one sequence.
func (runner *campaignRunner) probeReads(ctx context.Context) (readProbeResults, error) {
	missing := newFixture(runner.cfg.Run.ID, "exists-missing", 333)
	present, err := runner.serial.Exists(ctx, runner.source, runner.seed.digest)
	if err != nil || !present {
		return readProbeResults{}, fmt.Errorf("exists did not find independent seed: %w", err)
	}
	absent, err := runner.serial.Exists(ctx, runner.source, missing.digest)
	if err != nil || absent {
		return readProbeResults{}, fmt.Errorf("exists did not report fresh digest absent: %w", err)
	}
	existsResult := featureResult(FeatureExists, StatusPass, "", "present seed was true and fresh digest was false")

	progress := &progressProbe{}
	serialCapture := runner.observer.startPhase(FeatureSerialPull, false)
	err = pullExact(ctx, runner.serial, runner.source, runner.seed, blob.WithProgress(progress.callback))
	serialWire := serialCapture.finish()
	if err != nil {
		return readProbeResults{}, err
	}
	if progressErr := progress.validate(int64(len(runner.seed.data))); progressErr != nil {
		return readProbeResults{}, progressErr
	}
	serialResult := featureResult(
		FeatureSerialPull,
		StatusPass,
		"",
		"ORAS-seeded bytes and digest matched through verified EOF",
		Assertion{Name: "wire", OK: true, Observed: safeStatusSummary(serialWire.Events)},
	)
	progressResult := featureResult(
		FeatureProgress,
		StatusPass,
		"",
		"counts were monotonic, exact, and non-overlapping within one transfer",
	)

	rangeCapture := runner.observer.startPhase(FeaturePullRange, false)
	if rangeErr := probeRanges(ctx, runner.serial, runner.source, runner.seed); rangeErr != nil {
		rangeCapture.finish()
		return readProbeResults{}, rangeErr
	}
	rangeWire := rangeCapture.finish()
	pullRangeResult := featureResult(
		FeaturePullRange,
		StatusPass,
		"",
		"beginning, middle, and tail windows matched; past-end was rejected",
		Assertion{
			Name:     "range requests",
			OK:       countRangeRequests(rangeWire.Events) >= 3,
			Observed: strconv.Itoa(countRangeRequests(rangeWire.Events)),
		},
	)

	parallelCapture := runner.observer.startPhase(FeatureParallelPull, true)
	parallelProgress := &progressProbe{}
	err = pullExact(ctx, runner.parallel, runner.source, runner.seed, blob.WithProgress(parallelProgress.callback))
	parallelWire := parallelCapture.finish()
	if err != nil {
		return readProbeResults{}, err
	}
	if progressErr := parallelProgress.validate(int64(len(runner.seed.data))); progressErr != nil {
		return readProbeResults{}, fmt.Errorf("parallel Pull progress: %w", progressErr)
	}
	rangeRequests := countRangeRequests(parallelWire.Events)
	ignored := anyRangeStatus(parallelWire.Events, http.StatusOK)
	parallelSummary := "exact verified bytes returned after registry ignored ranges and the client fell back"
	if !ignored {
		if rangeRequests < 2 || parallelWire.MaximumActiveRanges < 2 || parallelWire.ActiveRanges != 0 {
			return readProbeResults{}, fmt.Errorf(
				"parallel Pull lacked concurrent ranged-body proof: ranges=%d max=%d active=%d",
				rangeRequests,
				parallelWire.MaximumActiveRanges,
				parallelWire.ActiveRanges,
			)
		}
		parallelSummary = fmt.Sprintf("exact verified bytes with %d ranged requests and max body overlap %d",
			rangeRequests, parallelWire.MaximumActiveRanges)
	}
	parallelResult := featureResult(FeatureParallelPull, StatusPass, "", parallelSummary)
	fallbackResult := featureResult(
		FeatureRangeFallback,
		StatusNotApplicable,
		"",
		"registry served every observed range request",
	)
	if ignored {
		fallbackResult = featureResult(
			FeatureRangeFallback,
			StatusPass,
			"observed",
			"registry ignored Range and exact single-stream fallback succeeded",
		)
	}

	resumeCapture := runner.observer.startPhase(FeatureResume, false)
	fault := &bodyFault{cutoff: max(1, int64(len(runner.seed.data))/3)}
	faultClient := blob.New(
		blob.WithTransport(&faultRoundTripper{next: runner.bundle.registry, fault: fault}),
		blob.WithStorageTransport(&faultRoundTripper{next: runner.bundle.storage, fault: fault}),
	)
	err = pullExact(ctx, faultClient, runner.source, runner.seed)
	resumeWire := resumeCapture.finish()
	if err != nil {
		return readProbeResults{}, fmt.Errorf("interrupted Pull did not recover: %w", err)
	}
	if !hasResumeRange(resumeWire.Events, fault.cutoff) {
		return readProbeResults{}, fmt.Errorf("interrupted Pull made no ranged continuation from byte %d", fault.cutoff)
	}
	resumeResult := featureResult(
		FeatureResume,
		StatusPass,
		"",
		fmt.Sprintf("injected failure after %d bytes resumed with Range and exact verified EOF", fault.cutoff),
	)

	return readProbeResults{
		exists: existsResult, serial: serialResult, progress: progressResult,
		pullRange: pullRangeResult, parallel: parallelResult,
		fallback: fallbackResult, resume: resumeResult,
	}, nil
}

// pullExact fully drains and closes a verified Pull reader.
func pullExact(
	ctx context.Context,
	client *blob.Client,
	repository blob.Repository,
	want fixture,
	options ...blob.TransferOption,
) error {
	reader, err := client.Pull(ctx, repository, want.digest, options...)
	if err != nil {
		if reader != nil {
			_ = reader.Close()
		}
		return fmt.Errorf("opening Pull for %s: %w", want.label, err)
	}
	got, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		return errors.Join(readErr, closeErr)
	}
	if !bytes.Equal(got, want.data) || digest.FromBytes(got) != want.digest {
		return fmt.Errorf("pull bytes or digest differed for %s", want.label)
	}
	return nil
}

// probeRanges checks three exact windows and one invalid past-end request.
func probeRanges(ctx context.Context, client *blob.Client, repository blob.Repository, want fixture) error {
	size := int64(len(want.data))
	windows := [][2]int64{
		{0, min(997, size)},
		{size / 3, min(4099, size-size/3)},
		{size - min(769, size), min(769, size)},
	}
	for _, window := range windows {
		reader, err := client.PullRange(ctx, repository, want.digest, window[0], window[1])
		if err != nil {
			if reader != nil {
				_ = reader.Close()
			}
			return err
		}
		got, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return err
		}
		if !bytes.Equal(got, want.data[window[0]:window[0]+window[1]]) {
			return fmt.Errorf("PullRange window at %d differed", window[0])
		}
	}
	reader, err := client.PullRange(ctx, repository, want.digest, size, 1)
	if reader != nil {
		_ = reader.Close()
	}
	if err == nil {
		return errors.New("PullRange accepted a past-end window")
	}
	crossingOffset := max(int64(0), size-10)
	reader, err = client.PullRange(ctx, repository, want.digest, crossingOffset, 20)
	if err == nil {
		_, err = io.ReadAll(reader)
	}
	if reader != nil {
		_ = reader.Close()
	}
	if err == nil {
		return errors.New("PullRange accepted a window crossing EOF")
	}
	return nil
}

// countRangeRequests counts requests carrying a byte Range.
func countRangeRequests(events []WireEvent) int {
	count := 0
	for _, event := range events {
		if event.Range != "" {
			count++
		}
	}
	return count
}

// anyRangeStatus reports whether a ranged request received status.
func anyRangeStatus(events []WireEvent, status int) bool {
	for _, event := range events {
		if event.Range != "" && event.Status == status {
			return true
		}
	}
	return false
}

// hasResumeRange finds a continuation beginning at or beyond cutoff.
func hasResumeRange(events []WireEvent, cutoff int64) bool {
	prefix := fmt.Sprintf("bytes=%d-", cutoff)
	for _, event := range events {
		if strings.HasPrefix(event.Range, prefix) {
			return true
		}
	}
	return false
}
