package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/andres1m/impuls-goroda/pkg/config"
	"github.com/andres1m/impuls-goroda/pkg/telemetry"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/adapter/kudago"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/adapter/mkrf"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/adapter/osm"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/kafka"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/repo/postgres"
)

const (
	sourceRequestTimeout = 3 * time.Minute
	expectedIngestArgs   = 2
	liveAdapterCount     = 3
)

func newAdapters(client *http.Client) map[domain.SourceKey]ingest.Adapter {
	adapters := make(map[domain.SourceKey]ingest.Adapter, liveAdapterCount)
	adapters[domain.MkrfEvents] = mkrf.New()
	adapters[domain.KudaGo] = kudago.New(kudago.DefaultBaseURL, client, time.Now)
	adapters[domain.OSM] = osm.New(osm.DefaultBaseURL, client, time.Now)
	return adapters
}

func parseIngestArgs(args []string, adapters map[domain.SourceKey]ingest.Adapter) (ingest.Adapter, domain.City, error) {
	keys := make([]string, 0, len(adapters))
	for key := range adapters {
		keys = append(keys, string(key))
	}
	slices.Sort(keys)
	usage := fmt.Errorf("usage: syncer ingest <%s> <moscow|perm>", strings.Join(keys, "|"))

	if len(args) != expectedIngestArgs {
		return nil, "", usage
	}
	adapter, ok := adapters[domain.SourceKey(args[0])]
	if !ok {
		return nil, "", errors.Join(fmt.Errorf("unknown source %q", args[0]), usage)
	}
	city, err := domain.ParseCity(args[1])
	if err != nil {
		return nil, "", errors.Join(err, usage)
	}
	return adapter, city, nil
}

func runIngest(ctx context.Context, args []string) (retErr error) {
	adapter, city, err := parseIngestArgs(args, newAdapters(&http.Client{Timeout: sourceRequestTimeout}))
	if err != nil {
		return err
	}

	var cfg appConfig
	if loadErr := config.Load(configPath, &cfg); loadErr != nil {
		return fmt.Errorf("load config: %w", loadErr)
	}
	// A one-shot run exports its spans too: the records it publishes carry its trace.
	tracing := telemetry.New("syncer", cfg.Telemetry, nil)
	if initErr := tracing.Init(ctx); initErr != nil {
		return initErr
	}
	defer func() {
		retErr = errors.Join(retErr, tracing.Stop(context.Background()))
	}()
	producer, err := kafka.NewProducer(cfg.Kafka)
	if err != nil {
		return err
	}
	defer producer.Close()

	database, err := openDatabase(ctx)
	if err != nil {
		return err
	}
	defer func() {
		retErr = errors.Join(retErr, database.Stop(context.Background()))
	}()

	service := ingest.NewService(postgres.NewLanding(database.Pool), producer, time.Now)
	result, err := service.Ingest(ctx, adapter, city)
	if err != nil {
		return err
	}
	fmt.Printf("%s %s: received %d, inserted %d, published %d, skipped %d\n",
		adapter.Source().Key, city, result.Received, result.Inserted, result.Published, result.Skipped)
	return nil
}
