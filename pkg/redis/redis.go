package redis

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/andres1m/impuls-goroda/pkg/config"
	"github.com/andres1m/impuls-goroda/pkg/svc"
	"github.com/redis/go-redis/extra/redisotel/v9"
	r "github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const redisModeCluster = "cluster"
const defaultConnectionTimeout = 5 * time.Second
const defaultCommandTimeout = 3 * time.Second

type RedisClient struct {
	cfg  config.Redis
	Pool r.UniversalClient
}

//nolint:gocritic // service stores and normalizes its own configuration copy
func NewRedis(_ *zap.Logger, cfg config.Redis) (*RedisClient, error) {
	if cfg.Mode != redisModeCluster && cfg.Mode != "standalone" {
		return nil, errors.New("redis mode must be cluster or standalone")
	}
	if len(cfg.Addresses) == 0 {
		return nil, errors.New("redis addresses are required")
	}
	if slices.Contains(cfg.Addresses, "") {
		return nil, errors.New("empty redis address")
	}
	if cfg.Mode == redisModeCluster && cfg.DB != 0 {
		return nil, errors.New("redis cluster requires database zero")
	}
	if cfg.Mode == "standalone" && len(cfg.Addresses) != 1 {
		return nil, errors.New("standalone redis requires one address")
	}
	if cfg.DB < 0 || cfg.ConnectionTimeout < 0 || cfg.ReadTimeout < 0 || cfg.WriteTimeout < 0 {
		return nil, errors.New("invalid redis database or timeout")
	}
	if cfg.ConnectionTimeout == 0 {
		cfg.ConnectionTimeout = defaultConnectionTimeout
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = defaultCommandTimeout
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = defaultCommandTimeout
	}
	cfg.Addresses = append([]string(nil), cfg.Addresses...)
	return &RedisClient{cfg: cfg}, nil
}
func (c *RedisClient) Name() string        { return "redis" }
func (c *RedisClient) DependsOn() []string { return []string{"logger"} }
func (c *RedisClient) Init(context.Context) error {
	if c.Pool != nil {
		return errors.New("redis already initialized")
	}
	o := &r.UniversalOptions{
		Addrs:        c.cfg.Addresses,
		Username:     c.cfg.Username,
		Password:     c.cfg.Password,
		DB:           c.cfg.DB,
		DialTimeout:  c.cfg.ConnectionTimeout,
		ReadTimeout:  c.cfg.ReadTimeout,
		WriteTimeout: c.cfg.WriteTimeout,
	}
	if c.cfg.Mode == redisModeCluster {
		cfg := o.Cluster()
		cfg.ContextTimeoutEnabled = true
		c.Pool = r.NewClusterClient(cfg)
	} else {
		cfg := o.Simple()
		cfg.ContextTimeoutEnabled = true
		c.Pool = r.NewClient(cfg)
	}
	// Command arguments hold keys and cached catalog slices; spans keep only the command name.
	if err := redisotel.InstrumentTracing(c.Pool, redisotel.WithDBStatement(false)); err != nil {
		_ = c.Pool.Close()
		c.Pool = nil
		return fmt.Errorf("redis tracing: %w", err)
	}
	return nil
}
func (c *RedisClient) HealthCheck(ctx context.Context) error {
	if c.Pool == nil {
		return errors.New("redis is not initialized")
	}
	if err := c.Pool.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping: %w", err)
	}
	return nil
}
func (c *RedisClient) Run(context.Context) error { return nil }
func (c *RedisClient) Stop(context.Context) error {
	if c.Pool == nil {
		return nil
	}
	err := c.Pool.Close()
	c.Pool = nil
	if err != nil {
		return fmt.Errorf("close redis: %w", err)
	}
	return nil
}

var _ svc.Service = (*RedisClient)(nil)
