package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/imgoci/go-oci-blob-compat/internal/campaign"
)

// main runs one subcommand and returns only safe operator diagnostics.
func main() {
	if err := execute(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// execute dispatches run and aggregate without retaining credential values.
func execute(arguments []string) error {
	if len(arguments) == 0 {
		return errors.New("usage: registry-compat <run|aggregate> [options]")
	}
	switch arguments[0] {
	case "run":
		return executeRun(arguments[1:])
	case "aggregate":
		return executeAggregate(arguments[1:])
	default:
		return fmt.Errorf("unknown subcommand %q", arguments[0])
	}
}

// executeRun loads one campaign, writes a valid or invalid redacted report,
// and returns nonzero for harness or infrastructure failures.
func executeRun(arguments []string) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", "", "campaign JSON path")
	outputPath := flags.String("output", "", "report JSON path")
	immutable := flags.Bool("immutable-consumer", false, "attest to a read-only exported source tree")
	if err := flags.Parse(arguments); err != nil {
		return errors.New("invalid run arguments")
	}
	if *configPath == "" || *outputPath == "" || flags.NArg() != 0 {
		return errors.New("run requires --config, --output, and no positional arguments")
	}
	cfg, err := campaign.LoadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("campaign configuration is invalid: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	report, secrets, runErr := campaign.Run(ctx, cfg, campaign.RunOptions{ImmutableConsumer: *immutable})
	if writeErr := campaign.WriteReport(*outputPath, report, secrets); writeErr != nil {
		return errors.New("campaign report could not be written safely")
	}
	if runErr != nil {
		return errors.New("campaign was invalid; inspect the redacted report diagnostics")
	}
	return nil
}

// executeAggregate combines fresh normal and race reports conservatively.
func executeAggregate(arguments []string) error {
	flags := flag.NewFlagSet("aggregate", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	outputPath := flags.String("output", "", "aggregate JSON path")
	if err := flags.Parse(arguments); err != nil {
		return errors.New("invalid aggregate arguments")
	}
	if *outputPath == "" || flags.NArg() < 2 {
		return errors.New("aggregate requires --output and at least one normal plus one race report")
	}
	aggregate, err := campaign.AggregateFiles(flags.Args())
	if err != nil {
		return fmt.Errorf("reports are not safely aggregatable: %w", err)
	}
	if err := campaign.WriteAggregate(*outputPath, aggregate, nil); err != nil {
		return errors.New("aggregate report could not be written safely")
	}
	return nil
}
