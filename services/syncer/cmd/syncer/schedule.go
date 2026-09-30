package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/kafka"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/repo/postgres"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/schedule"
)

// scheduledIngester builds the ingest service per run: the pool exists only once the database component
// has started.
type scheduledIngester struct {
	pool      func() *pgxpool.Pool
	publisher ingest.Publisher
}

func (s scheduledIngester) Ingest(
	ctx context.Context,
	adapter ingest.Adapter,
	city domain.City,
) (ingest.Result, error) {
	pool := s.pool()
	if pool == nil {
		return ingest.Result{}, errDatabaseNotConnected
	}
	result, err := ingest.NewService(postgres.NewLanding(pool), s.publisher, time.Now).Ingest(ctx, adapter, city)
	if err != nil {
		return result, fmt.Errorf("scheduled ingest: %w", err)
	}
	return result, nil
}

// newIngestScheduler returns nil when the configuration lists no jobs. The returned function releases what
// the scheduler holds and is always safe to call.
func newIngestScheduler(infra *infrastructureComponents) (*schedule.Scheduler, func(), error) {
	jobs, err := infra.cfg.Schedule.Parse()
	if err != nil {
		return nil, func() {}, err
	}
	if len(jobs) == 0 {
		return nil, func() {}, nil
	}
	producer, err := kafka.NewProducer(infra.cfg.Kafka)
	if err != nil {
		return nil, func() {}, fmt.Errorf("create ingest producer: %w", err)
	}
	pool := func() *pgxpool.Pool { return infra.pool.Pool }
	scheduler, err := schedule.New(
		jobs,
		newAdapters(&http.Client{Timeout: sourceRequestTimeout}),
		scheduledIngester{pool: pool, publisher: producer},
		postgres.NewSchedule(pool),
		infra.log.Log,
	)
	if err != nil {
		producer.Close()
		return nil, func() {}, err
	}
	return scheduler, producer.Close, nil
}
