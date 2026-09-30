package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/andres1m/impuls-goroda/pkg/config"
	"github.com/andres1m/impuls-goroda/pkg/temporal"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/repo/postgres"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/temporal/activity"
)

func parseSourceCityArgs(command string, args []string) (domain.SourceKey, domain.City, error) {
	usage := fmt.Errorf("usage: syncer %s <kudago|mkrf_events|osm> <moscow|perm>", command)
	const argCount = 2
	if len(args) != argCount {
		return "", "", usage
	}
	source := domain.SourceKey(args[0])
	switch source {
	case domain.KudaGo, domain.MkrfEvents, domain.OSM:
	case domain.SyntheticSource:
		return "", "", errors.Join(fmt.Errorf("unsupported source %q", args[0]), usage)
	default:
		return "", "", errors.Join(fmt.Errorf("unknown source %q", args[0]), usage)
	}
	city, err := domain.ParseCity(args[1])
	if err != nil {
		return "", "", errors.Join(err, usage)
	}
	return source, city, nil
}

func parseRematerializeArgs(args []string) (domain.SourceKey, domain.City, error) {
	return parseSourceCityArgs("rematerialize", args)
}

// rawSelector picks the raw records of a source and city that the city's materializing workflow has to
// be signalled about.
type rawSelector func(*postgres.MaterializeStore, context.Context, domain.SourceKey, domain.City) ([]string, error)

// runRematerialize runs the source's stored records of the city through materialization again, which moves
// the session horizon forward for records whose content has not changed.
func runRematerialize(ctx context.Context, args []string) error {
	return signalRawRecords(ctx, "rematerialize", args, (*postgres.MaterializeStore).Reopen)
}

// runReplay retries the records of a source and city that stopped as failed or quarantined or were never
// picked up, for example after a normalizer fix. Records outdated by a later version are retired without
// touching the catalog.
func runReplay(ctx context.Context, args []string) error {
	return signalRawRecords(ctx, "replay", args, (*postgres.MaterializeStore).Replay)
}

func signalRawRecords(ctx context.Context, command string, args []string, selectRaw rawSelector) (resultErr error) {
	source, city, err := parseSourceCityArgs(command, args)
	if err != nil {
		return err
	}
	var cfg appConfig
	if loadErr := config.Load(configPath, &cfg); loadErr != nil {
		return fmt.Errorf("load config: %w", loadErr)
	}
	database, err := openDatabase(ctx)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, database.Stop(context.Background())) }()
	workflows := temporal.NewClient(nil, nil, &cfg.Temporal)
	if initErr := workflows.Init(ctx); initErr != nil {
		return fmt.Errorf("connect temporal: %w", initErr)
	}
	defer func() { resultErr = errors.Join(resultErr, workflows.Stop(context.Background())) }()

	store := postgres.NewMaterializeStore(func() *pgxpool.Pool { return database.Pool })
	ids, err := selectRaw(store, ctx, source, city)
	if err != nil {
		return err
	}
	for i, id := range ids {
		if err := activity.SignalMaterialize(
			ctx,
			workflows.TemporalClient,
			workflows.TaskQueue(),
			city,
			id,
		); err != nil {
			return fmt.Errorf("signalled %d of %d: %w", i, len(ids), err)
		}
	}
	fmt.Printf("%s %s: signalled %d\n", source, city, len(ids))
	return nil
}
