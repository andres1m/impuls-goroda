package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/andres1m/impuls-goroda/pkg/ai"
	"github.com/andres1m/impuls-goroda/pkg/config"
	"github.com/andres1m/impuls-goroda/pkg/db"
	"github.com/andres1m/impuls-goroda/pkg/logger"
	"github.com/andres1m/impuls-goroda/pkg/router"
	"github.com/andres1m/impuls-goroda/pkg/rpc"
	"github.com/andres1m/impuls-goroda/pkg/server"
	"github.com/andres1m/impuls-goroda/pkg/svc"
	"github.com/andres1m/impuls-goroda/pkg/telemetry"
	gatewaypb "github.com/andres1m/impuls-goroda/proto/gateway/v1"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/app"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/auth"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/grpcapi"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/httpapi"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/maxbot"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/optimizerclient"
	"github.com/labstack/echo/v5"
)

const configPath = "config.yaml"

type appConfig struct {
	AI              *ai.Config         `yaml:"ai"`
	Logger          config.Logger      `yaml:"logger"`
	Telemetry       config.Telemetry   `yaml:"telemetry"`
	Database        config.Database    `yaml:"database"`
	Optimizer       config.GRPCClient  `yaml:"optimizer-client"`
	LifecycleServer *config.GRPCServer `yaml:"lifecycle-server"`
	APIServer       config.HTTPServer  `yaml:"api-server"`
	OpsServer       config.HTTPServer  `yaml:"ops-server"`
	Gateway         gatewayConfig      `yaml:"gateway"`
}

type gatewayConfig struct {
	CancellationWorker struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"cancellation-worker"`
	Computation optimizerclient.Policy `yaml:"computation"`
	Auth        gatewayAuthConfig      `yaml:"auth"`
	CORS        gatewayCORSConfig      `yaml:"cors"`
	RateLimit   rateLimitConfig        `yaml:"rate-limit"`
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
	cfg       *appConfig
	log       *logger.Log
	pool      *db.PostgresClient
	optimizer *optimizerclient.Client
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
	routers := []router.Router{
		httpapi.NewReadinessRouter(authRuntime),
		httpapi.NewAuthRouter(authRuntime),
		httpapi.NewVisitRouter(authRuntime),
		httpapi.NewRouteRouter(authRuntime),
		httpapi.NewNotificationPreferenceRouter(authRuntime),
		httpapi.NewDeleteRouter(authRuntime),
		httpapi.NewRecoveryRouter(authRuntime),
		httpapi.NewScenarioRouter(authRuntime),
		httpapi.NewPinRouter(authRuntime),
		httpapi.NewProposalRouter(authRuntime),
		httpapi.NewPanicRouter(authRuntime),
		httpapi.NewShareRouter(authRuntime, os.Getenv("MAX_BOT_USERNAME")),
		httpapi.NewDirectionsRouter(os.Getenv("TWO_GIS_API_KEY"), authRuntime),
		httpapi.NewLunchSearchRouter(os.Getenv("TWO_GIS_API_KEY"), authRuntime),
	}
	var botClient *maxbot.Client
	if username := os.Getenv("MAX_BOT_USERNAME"); username != "" {
		bot, err := maxbot.NewClient(infra.cfg.Gateway.Auth.BotToken, username)
		if err != nil {
			return fmt.Errorf("create MAX bot: %w", err)
		}
		botClient = bot
		var extractor *app.ScenarioExtractor
		if infra.cfg.AI != nil {
			models, err := ai.New(*infra.cfg.AI)
			if err != nil {
				return errors.New("invalid bot model configuration")
			}
			extractor = app.NewScenarioExtractor(models.Text())
		}
		routers = append(routers, httpapi.NewBotRouter(authRuntime, app.NewBotHandlerWithExtractor(authRuntime, bot, extractor)))
	}
	apiServer := server.New("api-server", infra.cfg.APIServer,
		server.WithIPExtractor(echo.ExtractIPFromXFFHeader(echo.TrustLinkLocal(false))),
		server.WithLogger(infra.log.Log),
		server.WithDependsOn("logger", "db", "gateway-auth"),
		server.WithHTTPErrorHandler(httpapi.ErrorHandler),
		server.WithMiddleware(httpapi.RequestIDMiddleware, cors),
		server.WithRouter(ctx, routers...),
		server.WithHealth(),
	)
	opsServer := server.New("ops-server", infra.cfg.OpsServer,
		server.WithHealth(),
		server.WithMetrics(),
	)

	components := []svc.Service{
		infra.log,
		telemetry.New("gateway", infra.cfg.Telemetry, infra.log.Log),
		infra.pool,
		infra.optimizer,
		authRuntime,
		apiServer,
		opsServer,
	}
	if authRuntime.NotificationDeliveryEnabled() {
		if botClient == nil || infra.cfg.LifecycleServer == nil {
			return errors.New("notification delivery requires bot and lifecycle receiver")
		}
		components = append(components, app.NewNotificationWorker(authRuntime, botClient))
	}
	if authRuntime.ScenarioResultDeliveryEnabled() {
		if botClient == nil {
			return errors.New("scenario result delivery requires a configured bot")
		}
		components = append(components, app.NewScenarioResultWorker(authRuntime, botClient))
	}
	if infra.cfg.Gateway.CancellationWorker.Enabled {
		if infra.cfg.LifecycleServer == nil {
			return errors.New("cancellation worker requires lifecycle receiver")
		}
		components = append(components, app.NewCancellationWorker(authRuntime))
	}
	if cfg := infra.cfg.LifecycleServer; cfg != nil {
		if cfg.Port <= 0 || cfg.Port > 65535 {
			return errors.New("invalid catalog lifecycle server port")
		}
		bounded := *cfg
		bounded.MaxRecvMsgSize = 64 * 1024
		lifecycleServer := rpc.NewServer("gateway-lifecycle", infra.log.Log, &bounded)
		lifecycleServer.OnInit(func(server *rpc.Server) {
			gatewaypb.RegisterLifecycleServiceServer(server.GetServer(), grpcapi.NewLifecycleServer(authRuntime))
		})
		components = append(components, lifecycleServer)
	}
	if err := svc.Run(ctx, infra.log.Log, components); err != nil {
		return fmt.Errorf("run service error: %w", err)
	}

	return nil
}

func newAuthRuntime(infra *infrastructureComponents) (*app.Runtime, error) {
	cfg := infra.cfg.Gateway
	var deliveryEvents bool
	if value, present := os.LookupEnv("GATEWAY_MAX_DELIVERY_EVENTS_ENABLED"); present {
		var err error
		deliveryEvents, err = strconv.ParseBool(value)
		if err != nil {
			return nil, errors.New("invalid MAX delivery events configuration")
		}
	}
	var notificationDelivery bool
	if deliveryEvents && os.Getenv("MAX_BOT_USERNAME") == "" {
		return nil, errors.New("MAX delivery events require a configured bot")
	}
	if value, present := os.LookupEnv("GATEWAY_NOTIFICATION_DELIVERY_ENABLED"); present {
		var err error
		notificationDelivery, err = strconv.ParseBool(value)
		if err != nil {
			return nil, errors.New("invalid notification delivery configuration")
		}
	}
	if notificationDelivery && !deliveryEvents {
		return nil, errors.New("notification delivery requires MAX delivery events")
	}
	var scenarioResultDelivery bool
	if value, present := os.LookupEnv("GATEWAY_SCENARIO_RESULT_DELIVERY_ENABLED"); present {
		var err error
		scenarioResultDelivery, err = strconv.ParseBool(value)
		if err != nil {
			return nil, errors.New("invalid scenario result delivery configuration")
		}
	}
	return app.NewRuntime(infra.pool, infra.log.Log, &app.Config{
		ScenarioResultDeliveryEnabled: scenarioResultDelivery,
		NotificationDeliveryEnabled:   notificationDelivery,
		MAXDeliveryEventsEnabled:      deliveryEvents,
		CancellationWorkerEnabled:     cfg.CancellationWorker.Enabled,
		LifecycleEnabled:              infra.cfg.LifecycleServer != nil,
		Optimizer:                     infra.optimizer,
		BotToken:                      cfg.Auth.BotToken,
		WebhookSecret:                 cfg.Auth.WebhookSecret,
		InitDataMaxAge:                cfg.Auth.InitDataMaxAge,
		InitDataFutureGap:             cfg.Auth.InitDataFutureGap,
		SessionTTL:                    cfg.Auth.SessionTTL,
		SessionCacheTTL:               cfg.Auth.SessionCacheTTL,
		SessionCacheSize:              cfg.Auth.SessionCacheSize,
		CleanupInterval:               cfg.Auth.CleanupInterval,
		CleanupBatchSize:              cfg.Auth.CleanupBatchSize,
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

const healthcheckTimeout = 3 * time.Second

func issueTestToken(ctx context.Context, args []string, output io.Writer) (retErr error) {
	flags := flag.NewFlagSet("issue-test-token", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	maxUserID := flags.String("account", "", "test account identifier")
	expiresAtValue := flags.String("expires-at", "", "RFC3339 expiry")
	if parseErr := flags.Parse(args); parseErr != nil {
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
	defer func() {
		retErr = errors.Join(retErr, infra.log.Stop(context.Background()))
	}()
	if initErr := infra.pool.Init(ctx); initErr != nil {
		return initErr
	}
	defer func() {
		retErr = errors.Join(retErr, infra.pool.Stop(context.Background()))
	}()
	runtime, err := newAuthRuntime(infra)
	if err != nil {
		return err
	}
	if initErr := runtime.Init(ctx); initErr != nil {
		return initErr
	}
	defer func() {
		retErr = errors.Join(retErr, runtime.Stop(context.Background()))
	}()
	issued, err := runtime.IssueTest(ctx, *maxUserID, expiresAt)
	if err != nil {
		return err
	}
	if _, writeErr := fmt.Fprintln(output, issued.AccessToken); writeErr != nil {
		return fmt.Errorf("write access token: %w", writeErr)
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

	pool, err := db.NewDB(zapLog.Log, cfg.Database)
	if err != nil {
		return nil, fmt.Errorf("create db error: %w", err)
	}

	optimizer, err := optimizerclient.NewWithPolicy(zapLog.Log, &cfg.Optimizer, cfg.Gateway.Computation)
	if err != nil {
		return nil, fmt.Errorf("create optimizer client error: %w", err)
	}
	return &infrastructureComponents{
		cfg:       &cfg,
		log:       zapLog,
		pool:      pool,
		optimizer: optimizer,
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

	ctx, cancel := context.WithTimeout(ctx, healthcheckTimeout)
	defer cancel()

	return server.Probe(ctx, fmt.Sprintf("http://127.0.0.1:%d/healthz", cfg.OpsServer.Port))
}
