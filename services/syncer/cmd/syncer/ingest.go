package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/adapter/kudago"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/adapter/mkrf"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/adapter/osm"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/repo/postgres"
)

const sourceRequestTimeout = 3 * time.Minute

func newAdapters(client *http.Client) map[domain.SourceKey]ingest.Adapter {
	return map[domain.SourceKey]ingest.Adapter{
		domain.MkrfEvents: mkrf.New(),
		domain.KudaGo:     kudago.New(kudago.DefaultBaseURL, client, time.Now),
		domain.OSM:        osm.New(osm.DefaultBaseURL, client, time.Now),
	}
}

func parseIngestArgs(args []string, adapters map[domain.SourceKey]ingest.Adapter) (ingest.Adapter, domain.City, error) {
	keys := make([]string, 0, len(adapters))
	for key := range adapters {
		keys = append(keys, string(key))
	}
	slices.Sort(keys)
	usage := fmt.Errorf("usage: syncer ingest <%s> <moscow|perm>", strings.Join(keys, "|"))

	if len(args) != 2 {
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

func runIngest(ctx context.Context, args []string) error {
	adapter, city, err := parseIngestArgs(args, newAdapters(&http.Client{Timeout: sourceRequestTimeout}))
	if err != nil {
		return err
	}

	database, err := openDatabase(ctx)
	if err != nil {
		return err
	}
	defer database.Stop(context.Background())

	service := ingest.NewService(postgres.NewLanding(database.Pool), time.Now)
	result, err := service.Ingest(ctx, adapter, city)
	if err != nil {
		return err
	}
	fmt.Printf("%s %s: received %d, inserted %d, skipped %d\n",
		adapter.Source().Key, city, result.Received, result.Inserted, result.Skipped)
	return nil
}
