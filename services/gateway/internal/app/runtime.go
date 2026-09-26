package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/andres1m/impuls-goroda/pkg/db"
	"github.com/andres1m/impuls-goroda/pkg/svc"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/auth"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"go.uber.org/zap"
)

type Config struct {
	BotToken           string
	WebhookSecret      string
	InitDataMaxAge     time.Duration
	InitDataFutureGap  time.Duration
	SessionTTL         time.Duration
	SessionCacheTTL    time.Duration
	SessionCacheSize   int64
	CleanupInterval    time.Duration
	CleanupBatchSize   int
	AnonymousLimit     auth.RateLimitConfig
	AuthenticatedLimit auth.RateLimitConfig
}

type Runtime struct {
	db    *db.PostgresClient
	log   *zap.Logger
	cfg   Config
	clock func() time.Time

	service       *auth.Service
	queries       *postgres.Queries
	commands      *postgres.CommandExecutor
	webhook       *auth.WebhookVerifier
	anonymous     *auth.RateLimiter
	authenticated *auth.RateLimiter
	stopOnce      sync.Once
}

func NewRuntime(database *db.PostgresClient, log *zap.Logger, cfg Config) (*Runtime, error) {
	if database == nil {
		return nil, errors.New("database client is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	if cfg.CleanupInterval <= 0 || cfg.CleanupBatchSize <= 0 {
		return nil, errors.New("invalid session cleanup configuration")
	}
	return &Runtime{db: database, log: log, cfg: cfg, clock: time.Now}, nil
}

func (r *Runtime) Name() string { return "gateway-auth" }

func (r *Runtime) DependsOn() []string { return []string{"db"} }

func (r *Runtime) Init(context.Context) error {
	if r.db.Pool == nil {
		return errors.New("database pool is not initialized")
	}
	queries, err := postgres.NewQueries(r.db.Pool)
	if err != nil {
		return err
	}
	transactor, err := postgres.NewTransactor(r.db.Pool)
	if err != nil {
		return err
	}
	commands, err := postgres.NewCommandExecutor(transactor)
	if err != nil {
		return err
	}
	verifier, err := auth.NewInitDataVerifier(
		r.cfg.BotToken,
		r.cfg.InitDataMaxAge,
		r.cfg.InitDataFutureGap,
		r.clock,
	)
	if err != nil {
		return err
	}
	cache, err := auth.NewRistrettoCache(r.cfg.SessionCacheSize)
	if err != nil {
		return fmt.Errorf("create session cache: %w", err)
	}
	service, err := auth.NewService(
		verifier,
		transactor,
		sessionReader{queries: queries},
		cache,
		auth.ServiceConfig{
			SessionTTL: r.cfg.SessionTTL,
			CacheTTL:   r.cfg.SessionCacheTTL,
			Clock:      r.clock,
		},
	)
	if err != nil {
		cache.Close()
		return err
	}
	webhook, err := auth.NewWebhookVerifier(r.cfg.WebhookSecret)
	if err != nil {
		service.Close()
		return err
	}
	anonymous, err := auth.NewRateLimiter(r.cfg.AnonymousLimit)
	if err != nil {
		service.Close()
		return err
	}
	authenticated, err := auth.NewRateLimiter(r.cfg.AuthenticatedLimit)
	if err != nil {
		service.Close()
		return err
	}

	r.queries = queries
	r.commands = commands
	r.service = service
	r.webhook = webhook
	r.anonymous = anonymous
	r.authenticated = authenticated
	return nil
}

func (r *Runtime) HealthCheck(context.Context) error {
	if r.service == nil || r.queries == nil || r.commands == nil || r.webhook == nil || r.anonymous == nil || r.authenticated == nil {
		return errors.New("gateway auth is not initialized")
	}
	return nil
}

func (r *Runtime) Run(ctx context.Context) error {
	cleanupSessions := time.NewTicker(r.cfg.CleanupInterval)
	cleanupLimiters := time.NewTicker(min(r.cfg.AnonymousLimit.IdleTTL, r.cfg.AuthenticatedLimit.IdleTTL) / 2)
	defer cleanupSessions.Stop()
	defer cleanupLimiters.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-cleanupSessions.C:
			if _, err := r.queries.DeleteExpiredSessions(ctx, now.UTC(), r.cfg.CleanupBatchSize); err != nil && ctx.Err() == nil {
				r.log.Error("failed to delete expired sessions", zap.Error(err))
			}
		case now := <-cleanupLimiters.C:
			r.anonymous.Cleanup(now)
			r.authenticated.Cleanup(now)
		}
	}
}

func (r *Runtime) Stop(context.Context) error {
	r.stopOnce.Do(func() {
		if r.service != nil {
			r.service.Close()
		}
	})
	return nil
}

func (r *Runtime) ExchangeMax(ctx context.Context, raw string) (auth.IssuedSession, error) {
	if r.service == nil {
		return auth.IssuedSession{}, errors.New("gateway auth is not initialized")
	}
	issued, err := r.service.ExchangeMax(ctx, raw)
	if errors.Is(err, postgres.ErrAccountDisabled) {
		return auth.IssuedSession{}, auth.ErrAuthRequired
	}
	return issued, err
}

func (r *Runtime) IssueTest(ctx context.Context, maxUserID string, expiresAt time.Time) (auth.IssuedSession, error) {
	if r.service == nil {
		return auth.IssuedSession{}, errors.New("gateway auth is not initialized")
	}
	return r.service.IssueTest(ctx, maxUserID, expiresAt)
}

func (r *Runtime) Authenticate(ctx context.Context, token string) (auth.SessionAccount, error) {
	if r.service == nil {
		return auth.SessionAccount{}, errors.New("gateway auth is not initialized")
	}
	return r.service.Authenticate(ctx, token)
}

func (r *Runtime) AllowAnonymous(key string, now time.Time) (bool, time.Duration) {
	if r.anonymous == nil {
		return false, time.Second
	}
	return r.anonymous.Allow(key, now)
}

func (r *Runtime) AllowAuthenticated(userID domain.UserID, now time.Time) (bool, time.Duration) {
	if r.authenticated == nil {
		return false, time.Second
	}
	return r.authenticated.Allow(string(userID[:]), now)
}

func (r *Runtime) WebhookValid(candidate string) bool {
	return r.webhook != nil && r.webhook.Valid(candidate)
}

func (r *Runtime) FindRouteAccess(ctx context.Context, routeID domain.RouteID, userID domain.UserID) (postgres.RouteAccess, error) {
	if r.queries == nil {
		return postgres.RouteAccess{}, errors.New("gateway auth is not initialized")
	}
	return r.queries.FindRouteAccess(ctx, routeID, userID)
}

func (r *Runtime) RequireRouteOwner(ctx context.Context, routeID domain.RouteID, userID domain.UserID) error {
	_, err := r.FindRouteAccess(ctx, routeID, userID)
	return err
}

type sessionReader struct {
	queries *postgres.Queries
}

func (r sessionReader) FindSessionByTokenHash(ctx context.Context, hash [32]byte) (auth.SessionAccount, error) {
	pair, err := r.queries.FindSessionByTokenHash(ctx, hash)
	if err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			return auth.SessionAccount{}, auth.ErrAuthRequired
		}
		return auth.SessionAccount{}, err
	}
	return auth.SessionAccount{Session: pair.Session, Account: pair.Account}, nil
}

var _ svc.Service = (*Runtime)(nil)
