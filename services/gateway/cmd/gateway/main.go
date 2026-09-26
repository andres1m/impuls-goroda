package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/andres1m/impuls-goroda/pkg/config"
	"github.com/andres1m/impuls-goroda/pkg/db"
	"github.com/andres1m/impuls-goroda/pkg/logger"
	"github.com/andres1m/impuls-goroda/pkg/server"
	"github.com/andres1m/impuls-goroda/pkg/svc"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/app"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/auth"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/httpapi"
)

const configPath = "config.yaml"

type appConfig struct {
	Logger    config.Logger     `yaml:"logger"`
	Database  config.Database   `yaml:"database"`
	APIServer config.HTTPServer `yaml:"api-server"`
	OpsServer config.HTTPServer `yaml:"ops-server"`
	Gateway   gatewayConfig     `yaml:"gateway"`
}

type gatewayConfig struct {
	Auth      gatewayAuthConfig `yaml:"auth"`
	CORS      gatewayCORSConfig `yaml:"cors"`
	RateLimit rateLimitConfig   `yaml:"rate-limit"`
}

type gatewayAuthConfig struct {
	BotToken          string        `yaml:"bot-token"`
	WebhookSecret     string        `yaml:"webhook-secret"`
	InitDataMaxAge    time.Duration `yaml:"init-data-max-age"`
	InitDataFutureGap time.Duration `yaml:"init-data-future-gap"`
	SessionTTL        time.Duration `yaml:"session-ttl"`
	SessionCacheTTL   time.Duration `yaml:"session-cache-ttl"`
	SessionCacheSize  int64         `yaml:"session-cache-size"`
	CleanupInterval   time.Duration `yaml:"cleanup-interval"`
	CleanupBatchSize  int           `yaml:"cleanup-batch-size"`
}

type gatewayCORSConfig struct {
	AllowedOrigin string `yaml:"allowed-origin"`
}

type rateLimitConfig struct {
	Anonymous     bucketConfig  `yaml:"anonymous"`
	Authenticated bucketConfig  `yaml:"authenticated"`
	IdleTTL       time.Duration `yaml:"idle-ttl"`
}

type bucketConfig struct {
	RequestsPerMinute int `yaml:"requests-per-minute"`
	Burst             int `yaml:"burst"`
}

type infrastructureComponents struct {
	cfg  *appConfig
	log  *logger.Log
	pool *db.PostgresClient
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
	if len(os.Args) > 1 && os.Args[1] == "issue-test-token" {
		if err := issueTestToken(ctx, os.Args[2:], os.Stdout); err != nil {
			log.Printf("issue test token failed: %v", err)
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

	authRuntime, err := newAuthRuntime(infra)
	if err != nil {
		return fmt.Errorf("create gateway auth: %w", err)
	}
	cors, err := httpapi.CORS(infra.cfg.Gateway.CORS.AllowedOrigin)
	if err != nil {
		return fmt.Errorf("create CORS middleware: %w", err)
	}
	apiServer := server.New("api-server", infra.cfg.APIServer,
		server.WithLogger(infra.log.Log),
		server.WithDependsOn("logger", "db", "gateway-auth"),
		server.WithHTTPErrorHandler(httpapi.ErrorHandler),
		server.WithMiddleware(httpapi.RequestIDMiddleware, cors),
		server.WithRouter(ctx, httpapi.NewAuthRouter(authRuntime), httpapi.NewVisitRouter(authRuntime)),
		server.WithHealth(),
	)
	opsServer := server.New("ops-server", infra.cfg.OpsServer,
		server.WithHealth(),
		server.WithMetrics(),
	)

	if err := svc.Run(ctx, infra.log.Log, []svc.Service{
		infra.log,
		infra.pool,
		authRuntime,
		apiServer,
		opsServer,
	}); err != nil {
		return fmt.Errorf("run service error: %w", err)
	}

	return nil
}

func newAuthRuntime(infra *infrastructureComponents) (*app.Runtime, error) {
	cfg := infra.cfg.Gateway
	return app.NewRuntime(infra.pool, infra.log.Log, app.Config{
		BotToken:          cfg.Auth.BotToken,
		WebhookSecret:     cfg.Auth.WebhookSecret,
		InitDataMaxAge:    cfg.Auth.InitDataMaxAge,
		InitDataFutureGap: cfg.Auth.InitDataFutureGap,
		SessionTTL:        cfg.Auth.SessionTTL,
		SessionCacheTTL:   cfg.Auth.SessionCacheTTL,
		SessionCacheSize:  cfg.Auth.SessionCacheSize,
		CleanupInterval:   cfg.Auth.CleanupInterval,
		CleanupBatchSize:  cfg.Auth.CleanupBatchSize,
		AnonymousLimit: auth.RateLimitConfig{
			RequestsPerMinute: cfg.RateLimit.Anonymous.RequestsPerMinute,
			Burst:             cfg.RateLimit.Anonymous.Burst,
			IdleTTL:           cfg.RateLimit.IdleTTL,
		},
		AuthenticatedLimit: auth.RateLimitConfig{
			RequestsPerMinute: cfg.RateLimit.Authenticated.RequestsPerMinute,
			Burst:             cfg.RateLimit.Authenticated.Burst,
			IdleTTL:           cfg.RateLimit.IdleTTL,
		},
	})
}

func issueTestToken(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("issue-test-token", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	maxUserID := flags.String("account", "", "test account identifier")
	expiresAtValue := flags.String("expires-at", "", "RFC3339 expiry")
	if err := flags.Parse(args); err != nil {
		return errors.New("invalid issue-test-token arguments")
	}
	if *maxUserID == "" || *expiresAtValue == "" || flags.NArg() != 0 {
		return errors.New("--account and --expires-at are required")
	}
	expiresAt, err := time.Parse(time.RFC3339, *expiresAtValue)
	if err != nil {
		return errors.New("invalid --expires-at value")
	}

	infra, err := initInfrastructure()
	if err != nil {
		return err
	}
	defer func() { _ = infra.log.Stop(context.Background()) }()
	if err := infra.pool.Init(ctx); err != nil {
		return err
	}
	defer func() { _ = infra.pool.Stop(context.Background()) }()
	runtime, err := newAuthRuntime(infra)
	if err != nil {
		return err
	}
	if err := runtime.Init(ctx); err != nil {
		return err
	}
	defer func() { _ = runtime.Stop(context.Background()) }()
	issued, err := runtime.IssueTest(ctx, *maxUserID, expiresAt)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, issued.AccessToken)
	return err
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

	return &infrastructureComponents{
		cfg:  &cfg,
		log:  zapLog,
		pool: pool,
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
