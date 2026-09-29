package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/andres1m/impuls-goroda/pkg/ai"
	"github.com/andres1m/impuls-goroda/pkg/config"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/enrich"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/repo/postgres"
)

const (
	maxEnrichBatch     = 100
	defaultEnrichBatch = 20
)

func parseEnrichArgs(args []string) ([]domain.City, int, error) {
	usage := errors.New("usage: syncer enrich [--city moscow|perm] [--batch 1..100]")
	flags := flag.NewFlagSet("enrich", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	city := flags.String("city", "", "")
	batch := flags.Int("batch", defaultEnrichBatch, "")
	if err := flags.Parse(args); err != nil {
		return nil, 0, errors.Join(err, usage)
	}
	if flags.NArg() != 0 || *batch < 1 || *batch > maxEnrichBatch {
		return nil, 0, usage
	}
	if *city == "" {
		return []domain.City{domain.Moscow, domain.Perm}, *batch, nil
	}
	parsed, err := domain.ParseCity(*city)
	if err != nil {
		return nil, 0, errors.Join(err, usage)
	}
	return []domain.City{parsed}, *batch, nil
}

func runEnrich(ctx context.Context, args []string) (retErr error) {
	cities, batch, err := parseEnrichArgs(args)
	if err != nil {
		return err
	}
	var cfg appConfig
	if loadErr := config.Load(configPath, &cfg); loadErr != nil {
		return fmt.Errorf("load config: %w", loadErr)
	}
	models, err := ai.New(cfg.AI)
	if err != nil {
		return fmt.Errorf("ai config: %w", err)
	}
	database, err := openDatabase(ctx)
	if err != nil {
		return err
	}
	defer func() {
		retErr = errors.Join(retErr, database.Stop(context.Background()))
	}()

	store := postgres.NewEnrichmentStore(database.Pool)
	var failures error
	for _, city := range cities {
		summary, runErr := enrich.Run(ctx, store, models.Text(), city, batch, time.Now)
		if runErr != nil {
			return fmt.Errorf("enrich %s: %w", city, runErr)
		}
		failures = errors.Join(failures, reportEnrichment(city, &summary))
	}
	return failures
}

func reportEnrichment(city domain.City, s *enrich.Summary) error {
	fmt.Printf("%s: entities %d, enriched %d, fallback %d, failed %d, revisions %d\n",
		city, s.Entities, s.Enriched, s.Fallback, s.Failed, s.Published)
	if s.LastError != nil {
		fmt.Printf("%s: last model error: %v\n", city, s.LastError)
	}
	if s.Failed == 0 {
		return nil
	}
	for _, title := range s.FailedTitles {
		fmt.Printf("%s: no tags: %s\n", city, title)
	}
	return fmt.Errorf("%s: %d entities have no tags after model failure", city, s.Failed)
}
