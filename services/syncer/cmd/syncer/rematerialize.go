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

func parseRematerializeArgs(args []string) (domain.SourceKey, domain.City, error) {
	usage := errors.New("usage: syncer rematerialize <kudago|mkrf_events|osm> <moscow|perm>")
	if len(args) != 2 {
		return "", "", usage
	}
	source := domain.SourceKey(args[0])
	switch source {
	case domain.KudaGo, domain.MkrfEvents, domain.OSM:
	default:
		return "", "", errors.Join(fmt.Errorf("unknown source %q", args[0]), usage)
	}
	city, err := domain.ParseCity(args[1])
	if err != nil {
		return "", "", errors.Join(err, usage)
	}
	return source, city, nil
}

// runRematerialize runs the source's stored records of the city through materialization again, which moves
// the session horizon forward for records whose content has not changed.
func runRematerialize(ctx context.Context, args []string) error {
	source, city, err := parseRematerializeArgs(args)
	if err != nil {
		return err
	}
	var cfg appConfig
	if err := config.Load(configPath, &cfg); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	database, err := openDatabase(ctx)
	if err != nil {
		return err
	}
	defer database.Stop(context.Background())
	workflows := temporal.NewClient(nil, nil, &cfg.Temporal)
	if err := workflows.Init(ctx); err != nil {
		return fmt.Errorf("connect temporal: %w", err)
	}
	defer workflows.Stop(context.Background())

	ids, err := postgres.NewMaterializeStore(func() *pgxpool.Pool { return database.Pool }).Reopen(ctx, source, city)
	if err != nil {
		return err
	}
	for i, id := range ids {
		if err := activity.SignalMaterialize(ctx, workflows.TemporalClient, workflows.TaskQueue(), city, id); err != nil {
			return fmt.Errorf("signalled %d of %d: %w", i, len(ids), err)
		}
	}
	fmt.Printf("%s %s: signalled %d\n", source, city, len(ids))
	return nil
}
