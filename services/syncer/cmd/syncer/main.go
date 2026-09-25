package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/andres1m/impuls-goroda/pkg/config"
	"github.com/andres1m/impuls-goroda/pkg/db"
	"github.com/andres1m/impuls-goroda/pkg/logger"
	"github.com/andres1m/impuls-goroda/pkg/redis"
	"github.com/andres1m/impuls-goroda/pkg/server"
	"github.com/andres1m/impuls-goroda/pkg/svc"
	"github.com/andres1m/impuls-goroda/pkg/temporal"
)

const configPath = "config.yaml"

type appConfig struct {
	Logger    config.Logger     `yaml:"logger"`
	Database  config.Database   `yaml:"database"`
	Redis     config.Redis      `yaml:"redis"`
	Temporal  config.Temporal   `yaml:"temporal"`
	OpsServer config.HTTPServer `yaml:"ops-server"`
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

	if err := svc.Run(ctx, infra.log.Log, []svc.Service{
		infra.log,
		infra.pool,
		infra.redis,
		infra.temporal,
		opsServer,
	}); err != nil {
		return fmt.Errorf("run service error: %w", err)
	}

	return nil
}

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
