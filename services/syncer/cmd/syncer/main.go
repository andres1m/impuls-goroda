package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/andres1m/impuls-goroda/pkg/ai"
	"github.com/andres1m/impuls-goroda/pkg/catalogevent"
	"github.com/andres1m/impuls-goroda/pkg/config"
	"github.com/andres1m/impuls-goroda/pkg/db"
	"github.com/andres1m/impuls-goroda/pkg/logger"
	"github.com/andres1m/impuls-goroda/pkg/redis"
	"github.com/andres1m/impuls-goroda/pkg/server"
	"github.com/andres1m/impuls-goroda/pkg/svc"
	"github.com/andres1m/impuls-goroda/pkg/telemetry"
	"github.com/andres1m/impuls-goroda/pkg/temporal"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/delivery"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/kafka"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/repo/postgres"
	rawtemporal "github.com/andres1m/impuls-goroda/services/syncer/internal/temporal"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/temporal/activity"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/temporal/workflow"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	sdkworkflow "go.temporal.io/sdk/workflow"
)

const configPath = "config.yaml"

type appConfig struct {
	Logger    config.Logger     `yaml:"logger"`
	Telemetry config.Telemetry  `yaml:"telemetry"`
	Database  config.Database   `yaml:"database"`
	Redis     config.Redis      `yaml:"redis"`
	Temporal  config.Temporal   `yaml:"temporal"`
	OpsServer config.HTTPServer `yaml:"ops-server"`
	AI        ai.Config         `yaml:"ai"`
	Kafka     kafka.Config      `yaml:"kafka"`
	Delivery  delivery.Config   `yaml:"delivery"`
}

type infrastructureComponents struct {
	cfg      *appConfig
	log      *logger.Log
	pool     *db.PostgresClient
	redis    *redis.RedisClient
	temporal *temporal.Client
}

func main() {
	ctx := context.Background()

	if len(os.Args) > 1 && os.Args[1] == "ingest" {
		if err := runIngest(ctx, os.Args[2:]); err != nil {
			log.Fatalf("ingest: %v", err)
		}
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "seed" {
		if err := runSeed(ctx, os.Args[2:]); err != nil {
			log.Fatalf("seed: %v", err)
		}
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "embed" {
		if err := runEmbed(ctx, os.Args[2:]); err != nil {
			log.Fatalf("embed: %v", err)
		}
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "rematerialize" {
		if err := runRematerialize(ctx, os.Args[2:]); err != nil {
			log.Fatalf("rematerialize: %v", err)
		}
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := healthcheck(ctx); err != nil {
			log.Printf("healthcheck failed: %v", err)
			os.Exit(1)
		}
		return
	}

	if err := run(ctx); err != nil {
		log.Fatalf("application error: %v", err)
	}

	log.Println("success shutdown")
}

func run(ctx context.Context) error {
	infra, err := initInfrastructure()
	if err != nil {
		return fmt.Errorf("init infrastructure error: %w", err)
	}

	opsServer := server.New("ops-server", infra.cfg.OpsServer,
		server.WithHealth(),
		server.WithMetrics(),
	)

	temporalWorker, err := temporal.NewWorker(infra.log.Log, infra.temporal, &infra.cfg.Temporal, func(r worker.Registry) {
		r.RegisterWorkflow(workflow.ProcessRawIngest)
		r.RegisterWorkflowWithOptions(workflow.MaterializeCity, sdkworkflow.RegisterOptions{Name: activity.MaterializeWorkflowName})
		r.RegisterActivity(&activity.Activities{
			Client: func() client.Client { return infra.temporal.TemporalClient },
			Queue:  infra.temporal.TaskQueue(),
			Store:  postgres.NewMaterializeStore(func() *pgxpool.Pool { return infra.pool.Pool }),
			Now:    time.Now,
		})
	})
	if err != nil {
		return fmt.Errorf("create temporal worker error: %w", err)
	}
	starter := rawtemporal.NewStarter(func() client.Client { return infra.temporal.TemporalClient }, infra.temporal.TaskQueue())
	consumer := kafka.NewConsumer(infra.log.Log, infra.cfg.Kafka, starter)
	relay, err := delivery.NewRelay(infra.cfg.Delivery, postgres.NewDeliveries(poolDB{client: infra.pool}),
		map[string]delivery.Sender{
			catalogevent.Destination: delivery.NewRedisSender(func() delivery.Publisher {
				if infra.redis.Pool == nil {
					return nil
				}
				return infra.redis.Pool
			}),
		}, infra.log.Log)
	if err != nil {
		return fmt.Errorf("create delivery relay error: %w", err)
	}

	if err := svc.Run(ctx, infra.log.Log, []svc.Service{
		infra.log,
		telemetry.New("syncer", infra.cfg.Telemetry, infra.log.Log),
		infra.pool,
		infra.redis,
		infra.temporal,
		temporalWorker,
		consumer,
		relay,
		opsServer,
	}); err != nil {
		return fmt.Errorf("run service error: %w", err)
	}

	return nil
}

var errDatabaseNotConnected = errors.New("database is not connected")

// poolDB reaches the connection pool, which exists only once the database component has started.
type poolDB struct {
	client *db.PostgresClient
}

func (p poolDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if p.client.Pool == nil {
		return nil, errDatabaseNotConnected
	}
	return p.client.Pool.Query(ctx, sql, args...)
}

func (p poolDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if p.client.Pool == nil {
		return notConnectedRow{}
	}
	return p.client.Pool.QueryRow(ctx, sql, args...)
}

func (p poolDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if p.client.Pool == nil {
		return pgconn.CommandTag{}, errDatabaseNotConnected
	}
	return p.client.Pool.Exec(ctx, sql, args...)
}

type notConnectedRow struct{}

func (notConnectedRow) Scan(...any) error { return errDatabaseNotConnected }

func initInfrastructure() (*infrastructureComponents, error) {
	var cfg appConfig
	if err := config.Load(configPath, &cfg); err != nil {
		return nil, fmt.Errorf("load config error: %w", err)
	}

	zapLog, err := newLogger(cfg.Logger)
	if err != nil {
		return nil, fmt.Errorf("create logger error: %w", err)
	}

	pool, err := db.NewDb(zapLog.Log, cfg.Database)
	if err != nil {
		return nil, fmt.Errorf("create db error: %w", err)
	}

	redisClient, err := redis.NewRedis(zapLog.Log, cfg.Redis)
	if err != nil {
		return nil, fmt.Errorf("create redis error: %w", err)
	}

	temporalClient := temporal.NewClient(zapLog.Log, nil, &cfg.Temporal)

	return &infrastructureComponents{
		cfg:      &cfg,
		log:      zapLog,
		pool:     pool,
		redis:    redisClient,
		temporal: temporalClient,
	}, nil
}

func newLogger(cfg config.Logger) (*logger.Log, error) {
	opts := []logger.Option{
		logger.WithLevel(cfg.Level),
		logger.WithStdOut(cfg.StdOut),
	}
	if cfg.File.Path != "" {
		opts = append(opts, logger.WithFile(cfg.File.Path))
	}

	return logger.New(opts...)
}

func healthcheck(ctx context.Context) error {
	var cfg appConfig
	if err := config.Load(configPath, &cfg); err != nil {
		return fmt.Errorf("load config error: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	return server.Probe(ctx, fmt.Sprintf("http://127.0.0.1:%d/healthz", cfg.OpsServer.Port))
}
