package db

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/andres1m/impuls-goroda/pkg/config"
	"github.com/andres1m/impuls-goroda/pkg/svc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type PostgresClient struct {
	log  *zap.Logger
	cfg  *pgxpool.Config
	Pool *pgxpool.Pool

	afterRunFuncs []AfterRun
	stopOnce      sync.Once
	stopped       chan struct{}
}

func (c *PostgresClient) DependsOn() []string {
	return []string{"logger"}
}

func (c *PostgresClient) HealthCheck(ctx context.Context) error {
	if c.Pool == nil {
		return errors.New("database pool is not initialized")
	}

	if err := c.Pool.Ping(ctx); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	c.log.Debug("database health check passed")

	return nil
}

func (c *PostgresClient) Init(ctx context.Context) error {
	if c.Pool != nil {
		return errors.New("database already initialized")
	}
	pool, err := pgxpool.NewWithConfig(ctx, c.cfg)
	if err != nil {
		return fmt.Errorf("failed to create database pool: %w", err)
	}

	c.Pool = pool

	for _, f := range c.afterRunFuncs {
		if err := f(ctx, pool); err != nil {
			pool.Close()
			c.Pool = nil
			return fmt.Errorf("failed to run initialization hook: %w", err)
		}
	}

	c.afterRunFuncs = nil

	c.log.Debug("database initialized")

	return nil
}

func (c *PostgresClient) Name() string {
	return "db"
}
func (c *PostgresClient) Run(ctx context.Context) error {
	return nil
}

func (c *PostgresClient) Stop(ctx context.Context) error {
	c.stopOnce.Do(func() {
		c.stopped = make(chan struct{})
		pool := c.Pool
		c.Pool = nil
		go func() {
			if pool != nil {
				pool.Close()
			}
			close(c.stopped)
		}()
	})
	select {
	case <-c.stopped:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type AfterRun func(ctx context.Context, pool *pgxpool.Pool) error

func NewDb(log *zap.Logger, conf config.Database) (*PostgresClient, error) {
	cfg, err := pgxpool.ParseConfig(conf.Host)
	if err != nil {
		return nil, errors.New("invalid database connection configuration")
	}

	if log == nil {
		log = zap.NewNop()
	}
	if conf.MaxConns < 0 || conf.MinConns < 0 || conf.ConnectionTimeout < 0 || conf.MaxConnLifetime < 0 {
		return nil, errors.New("invalid database pool limits")
	}
	if conf.ConnectionTimeout > 0 {
		cfg.ConnConfig.ConnectTimeout = conf.ConnectionTimeout
	}
	if conf.MaxConns > 0 {
		cfg.MaxConns = conf.MaxConns
	}
	cfg.MinConns = conf.MinConns
	if conf.MaxConnLifetime > 0 {
		cfg.MaxConnLifetime = conf.MaxConnLifetime
	}
	if cfg.MinConns > cfg.MaxConns {
		return nil, errors.New("min connections exceeds max connections")
	}

	return &PostgresClient{
		log:           log,
		cfg:           cfg,
		afterRunFuncs: make([]AfterRun, 0),
	}, nil
}

func (c *PostgresClient) AddAfterRun(f ...AfterRun) {
	c.afterRunFuncs = append(c.afterRunFuncs, f...)
}

func (c *PostgresClient) ConnConfig() *pgx.ConnConfig {
	return c.cfg.ConnConfig.Copy()
}

var _ svc.Service = (*PostgresClient)(nil)
