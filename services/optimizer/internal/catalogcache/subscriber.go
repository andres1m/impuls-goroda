package catalogcache

import (
	"context"
	"errors"
	"net"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/andres1m/impuls-goroda/pkg/catalogevent"
	"github.com/andres1m/impuls-goroda/pkg/svc"
)

// PubSub is one subscription to catalog announcements.
type PubSub interface {
	Receive(ctx context.Context, timeout time.Duration) (any, error)
	Ping(ctx context.Context) error
	Close() error
}

// silentPings is how many health intervals without any reply mean the connection is dead.
const silentPings = 3

// Subscriber keeps the cache told of new revisions. The cache trusts its memory only between a
// confirmed subscription followed by a successful comparison with the database and the next sign
// that announcements may have been missed.
type Subscriber struct {
	cache *Cache
	open  func(ctx context.Context) PubSub
	log   *zap.Logger
	// Set from the first failure until announcements are trusted again, so that an outage is
	// reported once rather than on every attempt.
	failing bool

	// Test hooks.
	pause      func(ctx context.Context, d time.Duration)
	afterReply func()
}

func NewSubscriber(cache *Cache, open func(ctx context.Context) PubSub, log *zap.Logger) *Subscriber {
	return &Subscriber{cache: cache, open: open, log: log, pause: sleep, afterReply: func() {}}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func (s *Subscriber) Name() string                      { return "catalog-subscriber" }
func (s *Subscriber) DependsOn() []string               { return []string{"logger", "db", "redis"} }
func (s *Subscriber) Init(context.Context) error        { return nil }
func (s *Subscriber) HealthCheck(context.Context) error { return nil }
func (s *Subscriber) Stop(context.Context) error        { return nil }

func (s *Subscriber) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		ps := s.open(ctx)
		s.listen(ctx, ps)
		if err := ps.Close(); err != nil {
			s.log.Debug("close catalog subscription", zap.Error(err))
		}
		s.cache.SetHealthy(false)
		s.pause(ctx, min(time.Second, s.cache.cfg.HealthInterval))
	}
	return nil
}

// listen returns when the connection fails or looks dead, so that a new one is opened.
func (s *Subscriber) listen(ctx context.Context, ps PubSub) {
	cfg := s.cache.cfg
	subscribed := false
	// When to compare with the database next: soon after a failed comparison, then periodically.
	var nextReconcile time.Time
	seen := s.cache.now()
	for {
		msg, err := ps.Receive(ctx, cfg.HealthInterval)
		if ctx.Err() != nil {
			return
		}
		now := s.cache.now()
		switch {
		case isTimeout(err):
			if now.Sub(seen) > silentPings*cfg.HealthInterval {
				s.lost("no reply from redis", nil)
				return
			}
			if err := ps.Ping(ctx); err != nil {
				s.lost("ping catalog subscription", err)
				return
			}
		case err != nil:
			s.lost("read catalog subscription", err)
			return
		default:
			seen = now
			switch m := msg.(type) {
			case *goredis.Subscription:
				if m.Kind == "subscribe" {
					subscribed = true
					nextReconcile = now
				}
			case *goredis.Message:
				s.announce(m.Payload)
			}
		}
		if subscribed && !now.Before(nextReconcile) {
			nextReconcile = now.Add(cfg.ReconcileInterval)
			if !s.reconcile(ctx) {
				nextReconcile = now.Add(cfg.HealthInterval)
			}
		}
		s.afterReply()
	}
}

func (s *Subscriber) lost(what string, err error) {
	s.cache.SetHealthy(false)
	s.warn(what, err)
	s.afterReply()
}

// reconcile trusts memory again only once every known city matches the database.
func (s *Subscriber) reconcile(ctx context.Context) bool {
	if err := s.cache.Reconcile(ctx); err != nil {
		s.cache.SetHealthy(false)
		s.warn("reconcile catalog revisions", err)
		return false
	}
	if s.failing {
		s.log.Info("catalog announcements are trusted again")
		s.failing = false
	}
	s.cache.SetHealthy(true)
	return true
}

func (s *Subscriber) warn(what string, err error) {
	if s.failing {
		s.log.Debug(what, zap.Error(err))
		return
	}
	s.failing = true
	s.log.Warn(what, zap.Error(err))
}

func (s *Subscriber) announce(payload string) {
	m, err := catalogevent.Decode([]byte(payload))
	if err != nil {
		invalidationErrors.Inc()
		s.log.Warn("catalog announcement", zap.Error(err))
		return
	}
	s.cache.Announce(m)
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

type redisPubSub struct {
	ps *goredis.PubSub
}

func (r redisPubSub) Receive(ctx context.Context, timeout time.Duration) (any, error) {
	return r.ps.ReceiveTimeout(ctx, timeout)
}

func (r redisPubSub) Ping(ctx context.Context) error { return r.ps.Ping(ctx) }
func (r redisPubSub) Close() error                   { return r.ps.Close() }

type disconnected struct{}

func (disconnected) Receive(context.Context, time.Duration) (any, error) {
	return nil, errRedisNotConnected
}
func (disconnected) Ping(context.Context) error { return errRedisNotConnected }
func (disconnected) Close() error               { return nil }

// RedisPubSub subscribes to catalog announcements with the client the Redis component started.
func RedisPubSub(client func() goredis.UniversalClient) func(ctx context.Context) PubSub {
	return func(ctx context.Context) PubSub {
		c := client()
		if c == nil {
			return disconnected{}
		}
		return redisPubSub{ps: c.Subscribe(ctx, catalogevent.Channel)}
	}
}

var _ svc.Service = (*Subscriber)(nil)
