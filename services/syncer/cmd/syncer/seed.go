package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/andres1m/impuls-goroda/pkg/config"
	"github.com/andres1m/impuls-goroda/pkg/db"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/repo/postgres"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/seed"
)

func parseSeedArgs(args []string) (string, error) {
	usage := errors.New("usage: syncer seed [--from YYYY-MM-DD]")
	flags := flag.NewFlagSet("seed", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	from := flags.String("from", "", "")
	if err := flags.Parse(args); err != nil {
		return "", errors.Join(err, usage)
	}
	if flags.NArg() != 0 {
		return "", usage
	}
	if *from != "" {
		if _, err := time.Parse(time.DateOnly, *from); err != nil {
			return "", errors.Join(fmt.Errorf("--from %q is not YYYY-MM-DD", *from), usage)
		}
	}
	return *from, nil
}

func runSeed(ctx context.Context, args []string) error {
	from, err := parseSeedArgs(args)
	if err != nil {
		return err
	}
	cities := []domain.City{domain.Moscow, domain.Perm}
	datasets := make(map[domain.City]seed.Dataset, len(cities))
	for _, city := range cities {
		if datasets[city], err = seed.Load(city); err != nil {
			return err
		}
	}

	database, err := openDatabase(ctx)
	if err != nil {
		return err
	}
	defer database.Stop(context.Background())

	now := time.Now()
	for _, city := range cities {
		err := pgx.BeginFunc(ctx, database.Pool, func(tx pgx.Tx) error {
			ref, err := postgres.LoadSeedReference(ctx, tx)
			if err != nil {
				return err
			}
			rows, err := seed.Expand(datasets[city], ref, from, now)
			if err != nil {
				return err
			}
			result, err := postgres.ApplySeed(ctx, tx, rows, now)
			if err != nil {
				return err
			}
			fmt.Printf("%s: places %d, events %d, sessions %d, prices %d, catalog revision %d\n",
				city, result.Places, result.Events, result.Sessions, result.Prices, result.CatalogRevision)
			return nil
		})
		if err != nil {
			return fmt.Errorf("seed %s: %w", city, err)
		}
	}
	return nil
}

func openDatabase(ctx context.Context) (*db.PostgresClient, error) {
	var cfg appConfig
	if err := config.Load(configPath, &cfg); err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	zapLog, err := newLogger(cfg.Logger)
	if err != nil {
		return nil, fmt.Errorf("create logger: %w", err)
	}
	database, err := db.NewDb(zapLog.Log, cfg.Database)
	if err != nil {
		return nil, fmt.Errorf("create db: %w", err)
	}
	if err := database.Init(ctx); err != nil {
		return nil, fmt.Errorf("connect db: %w", err)
	}
	return database, nil
}
