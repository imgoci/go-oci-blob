package campaign

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
)

// probeMount requires fresh destination absence immediately before the mount.
func (runner *campaignRunner) probeMount(ctx context.Context) (FeatureResult, error) {
	if err := proveAbsent(ctx, runner.raw, runner.cfg, runner.destination.Name, runner.seed.digest); err != nil {
		return FeatureResult{}, fmt.Errorf("destination was not proven absent immediately before Mount: %w", err)
	}
	capture := runner.observer.startPhase(FeatureMount, false)
	mounted, err := runner.serial.Mount(ctx, runner.destination, runner.source, runner.seed.digest)
	wire := capture.finish()
	if err != nil {
		return FeatureResult{}, fmt.Errorf("mount returned an unclassified error: %w", err)
	}
	if mounted {
		if !validMountSuccessWire(wire.Events) {
			return FeatureResult{}, errors.New("mount returned true without exactly one 201 POST and no upload body")
		}
		if err := runner.linkAndVerify(ctx, runner.destination, runner.seed); err != nil {
			return FeatureResult{}, fmt.Errorf("independent verification after Mount: %w", err)
		}
		return featureResult(
			FeatureMount,
			StatusPass,
			"",
			"fresh destination received a 201 mount with no upload body; raw HTTP and ORAS matched",
		), nil
	}
	if !validMountDeclineWire(wire.Events) {
		return FeatureResult{}, errors.New("mount returned false without one 202 and successful cleanup DELETE")
	}
	if err := proveAbsent(ctx, runner.raw, runner.cfg, runner.destination.Name, runner.seed.digest); err != nil {
		return FeatureResult{}, fmt.Errorf("declined Mount unexpectedly exposed the blob: %w", err)
	}
	if runner.featureBlocked(FeatureMount) {
		return featureResult(
			FeatureMount,
			StatusBlocked,
			"",
			"registry declined with 202 while an independently recorded external policy blocked mounting",
		), nil
	}
	return featureResult(
		FeatureMount,
		StatusNo,
		"",
		"registry declined a valid fresh mount with 202; cleanup DELETE succeeded and destination stayed absent",
	), nil
}

// featureBlocked reports an operator-attested external policy block.
func (runner *campaignRunner) featureBlocked(id string) bool {
	return slices.Contains(runner.cfg.Run.BlockedFeatures, id)
}

// validMountSuccessWire checks a single mount POST with no upload body verbs.
func validMountSuccessWire(events []WireEvent) bool {
	posts := 0
	for _, event := range events {
		if event.Endpoint != endpointUpload {
			continue
		}
		if event.Method == http.MethodPatch || event.Method == http.MethodPut || event.Method == http.MethodDelete {
			return false
		}
		if event.Method == http.MethodPost && event.Status == http.StatusCreated {
			posts++
		}
	}
	return posts == 1
}

// validMountDeclineWire checks one 202 session followed by safe cancellation.
func validMountDeclineWire(events []WireEvent) bool {
	posts := 0
	deletes := 0
	for _, event := range events {
		if event.Endpoint != endpointUpload {
			continue
		}
		if event.Method == http.MethodPost && event.Status == http.StatusAccepted {
			posts++
		}
		if event.Method == http.MethodDelete &&
			(event.Status == http.StatusNoContent || event.Status == http.StatusNotFound) {
			deletes++
		}
		if event.Method == http.MethodPatch || event.Method == http.MethodPut {
			return false
		}
	}
	return posts == 1 && deletes == 1
}
