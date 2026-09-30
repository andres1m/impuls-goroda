package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/coverage"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/repo/postgres"
)

// parseCoverageArgs returns the city to report on; empty means all cities.
func parseCoverageArgs(args []string) (string, error) {
	usage := errors.New("usage: syncer coverage [--city moscow|perm]")
	flags := flag.NewFlagSet("coverage", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	city := flags.String("city", "", "")
	if err := flags.Parse(args); err != nil {
		return "", errors.Join(err, usage)
	}
	if flags.NArg() != 0 {
		return "", usage
	}
	if *city == "" {
		return "", nil
	}
	parsed, err := domain.ParseCity(*city)
	if err != nil {
		return "", errors.Join(err, usage)
	}
	return string(parsed), nil
}

func runCoverage(ctx context.Context, args []string) (retErr error) {
	city, err := parseCoverageArgs(args)
	if err != nil {
		return err
	}
	database, err := openDatabase(ctx)
	if err != nil {
		return err
	}
	defer func() {
		retErr = errors.Join(retErr, database.Stop(context.Background()))
	}()
	now := time.Now()
	report, err := postgres.NewCoverage(database.Pool).Read(ctx, city, now)
	if err != nil {
		return err
	}
	if err := coverage.Render(os.Stdout, &report, now); err != nil {
		return fmt.Errorf("print coverage: %w", err)
	}
	return nil
}
