package campaign

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
)

// concurrentOperation is one barrier-started use of a shared Client.
type concurrentOperation struct {
	// name identifies failures without exposing resource URLs.
	name string
	// run executes the public library operation.
	run func(context.Context) error
}

// probeConcurrency runs a compact mixed corpus through one client and verifies
// every created artifact independently.
func (runner *campaignRunner) probeConcurrency(ctx context.Context) (FeatureResult, error) {
	fixtures := []fixture{
		newFixture(runner.cfg.Run.ID, "concurrent-push-a", 65_539),
		newFixture(runner.cfg.Run.ID, "concurrent-push-b", 131_081),
		newFixture(runner.cfg.Run.ID, "concurrent-push-c", 4099),
	}
	missing := newFixture(runner.cfg.Run.ID, "concurrent-missing", 701)
	operations := runner.concurrentOperations(fixtures, missing)
	capture := runner.observer.startPhase(FeatureConcurrency, false)
	if err := executeConcurrentOperations(ctx, operations); err != nil {
		capture.finish()
		return FeatureResult{}, err
	}
	wire := capture.finish()
	for _, value := range fixtures {
		if err := runner.linkAndVerify(ctx, runner.source, value); err != nil {
			return FeatureResult{}, fmt.Errorf("independent concurrent artifact verification: %w", err)
		}
	}
	return featureResult(
		FeatureConcurrency,
		StatusPass,
		"",
		fmt.Sprintf(
			"one Client completed %d barrier-started mixed operations; %d pushed artifacts were independently verified; %d wire requests observed",
			len(operations),
			len(fixtures),
			len(wire.Events),
		),
	), nil
}

// concurrentOperations builds the mixed public-API operations for one shared client.
func (runner *campaignRunner) concurrentOperations(
	fixtures []fixture,
	missing fixture,
) []concurrentOperation {
	operations := []concurrentOperation{
		{name: "pull", run: func(operationCtx context.Context) error {
			return pullExact(operationCtx, runner.serial, runner.source, runner.seed)
		}},
		{name: "range", run: func(operationCtx context.Context) error {
			reader, err := runner.serial.PullRange(operationCtx, runner.source, runner.seed.digest, 17, 1021)
			if err != nil {
				if reader != nil {
					_ = reader.Close()
				}
				return err
			}
			got, readErr := io.ReadAll(reader)
			closeErr := reader.Close()
			if readErr != nil || closeErr != nil || !bytes.Equal(got, runner.seed.data[17:1038]) {
				return errors.New("concurrent range differed")
			}
			return nil
		}},
		{name: "exists-present", run: func(operationCtx context.Context) error {
			ok, err := runner.serial.Exists(operationCtx, runner.source, runner.seed.digest)
			if err != nil || !ok {
				return fmt.Errorf("concurrent present Exists failed: %w", err)
			}
			return nil
		}},
		{name: "exists-missing", run: func(operationCtx context.Context) error {
			ok, err := runner.serial.Exists(operationCtx, runner.source, missing.digest)
			if err != nil || ok {
				return fmt.Errorf("concurrent missing Exists failed: %w", err)
			}
			return nil
		}},
	}
	for _, value := range fixtures {
		operations = append(
			operations,
			concurrentOperation{name: value.label, run: func(operationCtx context.Context) error {
				return runner.serial.Push(
					operationCtx,
					runner.source,
					value.digest,
					int64(len(value.data)),
					bytes.NewReader(value.data),
				)
			}},
		)
	}
	// Mount is included in the shared-client mix. Either a true mount or a clean
	// 202 decline is valid here because the dedicated probe classified support.
	operations = append(operations, concurrentOperation{name: "mount", run: func(operationCtx context.Context) error {
		_, err := runner.serial.Mount(operationCtx, runner.destination, runner.source, runner.seed.digest)
		return err
	}})
	return operations
}

// executeConcurrentOperations barrier-starts operations and joins every result
// under ctx without leaking workers past repository cleanup.
func executeConcurrentOperations(ctx context.Context, operations []concurrentOperation) error {
	start := make(chan struct{})
	errorsByOperation := make(chan error, len(operations))
	var ready sync.WaitGroup
	ready.Add(len(operations))
	for _, operation := range operations {
		go func() {
			ready.Done()
			select {
			case <-ctx.Done():
				errorsByOperation <- fmt.Errorf("%s: %w", operation.name, ctx.Err())
				return
			case <-start:
			}
			errorsByOperation <- operation.run(ctx)
		}()
	}
	ready.Wait()
	close(start)
	var firstError error
	for range operations {
		select {
		case <-ctx.Done():
			return fmt.Errorf("concurrency campaign did not finish: %w", ctx.Err())
		case operationErr := <-errorsByOperation:
			if operationErr != nil && firstError == nil {
				firstError = operationErr
			}
		}
	}
	return firstError
}
