// Package catalogcache keeps each city's catalog slice in memory and in Redis, so a request is planned
// without reading the whole catalog again. A slice is served without asking the database only while
// the subscription to catalog announcements is healthy and no newer revision has been heard of.
package catalogcache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dgraph-io/ristretto/v2"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	"github.com/andres1m/impuls-goroda/pkg/catalogevent"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/catalogslice"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/usecase"
)

// buildTimeout bounds a slice build. Builds belong to the cache, not to the request that started
// them: a cancelled request does not waste a build others are waiting for.
const buildTimeout = 30 * time.Second

type Loader interface {
	Revision(ctx context.Context, city string) (domain.CatalogRevision, error)
	LoadSlice(ctx context.Context, city string, horizon time.Time) (*catalogslice.Slice, error)
	SessionsByID(ctx context.Context, slice *catalogslice.Slice, ids []domain.SessionID) ([]domain.Candidate, error)
}

// SliceStore is the cache shared by optimizer instances.
type SliceStore interface {
	Get(ctx context.Context, city string, revision domain.CatalogRevision) (*catalogslice.Slice, bool, error)
	Put(ctx context.Context, slice *catalogslice.Slice) error
}

type Config struct {
	L1MaxBytes int64         `yaml:"l1-max-bytes"`
	L2TTL      time.Duration `yaml:"l2-ttl"`
	// Sessions that ended this long before a slice is built are left out of it.
	SessionHorizon    time.Duration `yaml:"session-horizon"`
	HealthInterval    time.Duration `yaml:"health-interval"`
	ReconcileInterval time.Duration `yaml:"reconcile-interval"`
}

func (c Config) withDefaults() Config {
	if c.L1MaxBytes == 0 {
		c.L1MaxBytes = 256 << 20
	}
	if c.L2TTL == 0 {
		c.L2TTL = 24 * time.Hour
	}
	if c.SessionHorizon == 0 {
		c.SessionHorizon = 24 * time.Hour
	}
	if c.HealthInterval == 0 {
		c.HealthInterval = 5 * time.Second
	}
	if c.ReconcileInterval == 0 {
		c.ReconcileInterval = time.Minute
	}
	return c
}

func (c Config) validate() error {
	if c.L1MaxBytes <= 0 || c.L2TTL <= 0 || c.SessionHorizon <= 0 || c.HealthInterval <= 0 || c.ReconcileInterval <= 0 {
		return errors.New("catalog cache config needs a positive memory limit, TTL, horizon and intervals")
	}
	return nil
}

type Cache struct {
	cfg    Config
	loader Loader
	l2     SliceStore
	log    *zap.Logger
	now    func() time.Time
	l1     *ristretto.Cache[string, *catalogslice.Slice]
	builds singleflight.Group

	healthy atomic.Bool
	mu      sync.Mutex
	// The newest revision of each city heard of, from the database or an announcement.
	known map[string]domain.CatalogRevision

	// Builds and background warming stop with it.
	warmCtx   context.Context
	stopWarm  context.CancelFunc
	warmGroup sync.WaitGroup
	closeOnce sync.Once
}

type Option func(*Cache)

func WithClock(now func() time.Time) Option {
	return func(c *Cache) { c.now = now }
}

func New(cfg Config, loader Loader, l2 SliceStore, log *zap.Logger, opts ...Option) (*Cache, error) {
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if loader == nil || l2 == nil || log == nil {
		return nil, errors.New("catalog cache needs a loader, a shared store and a logger")
	}
	l1, err := ristretto.NewCache(&ristretto.Config[string, *catalogslice.Slice]{
		NumCounters: 1000, MaxCost: cfg.L1MaxBytes, BufferItems: 64, IgnoreInternalCost: true,
	})
	if err != nil {
		return nil, fmt.Errorf("create catalog slice cache: %w", err)
	}
	c := &Cache{cfg: cfg, loader: loader, l2: l2, log: log, now: time.Now, l1: l1, known: map[string]domain.CatalogRevision{}}
	c.warmCtx, c.stopWarm = context.WithCancel(context.Background())
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// Close stops slices being warmed in the background and drops the memory cache.
func (c *Cache) Close() {
	c.closeOnce.Do(func() {
		c.stopWarm()
		c.warmGroup.Wait()
		c.l1.Close()
	})
}

// Candidates expands the city's slice for the request.
func (c *Cache) Candidates(ctx context.Context, req domain.OptimizeRequest) ([]domain.Candidate, domain.DataFreshness, error) {
	if err := req.Validate(); err != nil {
		return nil, domain.DataFreshness{}, fmt.Errorf("%w: %v", usecase.ErrInvalidRequest, err)
	}
	slice, level, err := c.slice(ctx, req.City)
	if err != nil {
		return nil, domain.DataFreshness{}, err
	}
	if !slice.Covers(req) {
		// A day before the horizon is rare enough to read on its own, without evicting the city's slice.
		if slice, err = c.loader.LoadSlice(ctx, req.City, req.Start); err != nil {
			return nil, domain.DataFreshness{}, err
		}
		level = levelDB
	}
	var extra []domain.Candidate
	if missing := slice.Missing(req); len(missing) > 0 {
		if extra, err = c.loader.SessionsByID(ctx, slice, missing); err != nil {
			return nil, domain.DataFreshness{}, err
		}
	}
	sliceHits.WithLabelValues(level).Inc()
	return slice.Candidates(req, extra)
}

const (
	levelTrusted = "l1_trusted"
	levelChecked = "l1_checked"
	levelL2      = "l2"
	levelDB      = "db"
)

func (c *Cache) slice(ctx context.Context, city string) (*catalogslice.Slice, string, error) {
	if s, ok := c.l1.Get(city); ok && c.trusted(city, s.Revision) {
		return s, levelTrusted, nil
	}
	revision, err := c.loader.Revision(ctx, city)
	if err != nil {
		return nil, "", err
	}
	revisionChecks.Inc()
	c.Observe(city, revision)
	if s, ok := c.l1.Get(city); ok && s.Revision >= revision {
		return s, levelChecked, nil
	}
	return c.obtain(ctx, city, revision)
}

func (c *Cache) trusted(city string, revision domain.CatalogRevision) bool {
	if !c.healthy.Load() {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.known[city] == revision
}

type obtained struct {
	slice *catalogslice.Slice
	level string
}

// obtain gets the slice of at least the revision from the shared store or the database; one build
// per city runs at a time and the others wait for it. Joining a build that started before the
// revision was heard of can yield an older slice, so that case builds once more.
func (c *Cache) obtain(ctx context.Context, city string, revision domain.CatalogRevision) (*catalogslice.Slice, string, error) {
	s, level, err := c.build(ctx, city, revision)
	if err == nil && s.Revision < revision {
		s, level, err = c.build(ctx, city, revision)
	}
	return s, level, err
}

func (c *Cache) build(ctx context.Context, city string, revision domain.CatalogRevision) (*catalogslice.Slice, string, error) {
	ch := c.builds.DoChan(city, func() (any, error) {
		if s, ok := c.l1.Get(city); ok && s.Revision >= revision {
			return obtained{s, levelChecked}, nil
		}
		ctx, cancel := context.WithTimeout(c.warmCtx, buildTimeout)
		defer cancel()
		if s, ok, err := c.l2.Get(ctx, city, revision); err != nil {
			l2Errors.WithLabelValues("get").Inc()
			c.log.Warn("read catalog slice from the shared cache", zap.String("city", city), zap.Error(err))
		} else if ok {
			c.keep(s)
			return obtained{s, levelL2}, nil
		}
		started := time.Now()
		s, err := c.loader.LoadSlice(ctx, city, c.now().Add(-c.cfg.SessionHorizon))
		if err != nil {
			return nil, err
		}
		buildSeconds.Observe(time.Since(started).Seconds())
		c.keep(s)
		if err := c.l2.Put(ctx, s); err != nil {
			l2Errors.WithLabelValues("put").Inc()
			c.log.Warn("write catalog slice to the shared cache", zap.String("city", city), zap.Error(err))
		}
		return obtained{s, levelDB}, nil
	})
	select {
	case <-ctx.Done():
		return nil, "", ctx.Err()
	case r := <-ch:
		if r.Err != nil {
			return nil, "", r.Err
		}
		o := r.Val.(obtained)
		return o.slice, o.level, nil
	}
}

// keep puts the slice in memory unless a newer one is there already.
func (c *Cache) keep(s *catalogslice.Slice) {
	c.Observe(s.City, s.Revision)
	if cur, ok := c.l1.Get(s.City); ok && cur.Revision >= s.Revision {
		return
	}
	c.l1.Set(s.City, s, s.Size())
	c.l1.Wait()
	sliceRevision.WithLabelValues(s.City).Set(float64(s.Revision))
}

// Observe notes that the city's catalog reached the revision, so older slices are no longer trusted.
func (c *Cache) Observe(city string, revision domain.CatalogRevision) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if revision > c.known[city] {
		c.known[city] = revision
	}
}

// SetHealthy tells whether announcements currently reach the cache.
func (c *Cache) SetHealthy(healthy bool) {
	c.healthy.Store(healthy)
	value := 0.0
	if healthy {
		value = 1
	}
	subscriptionHealthy.Set(value)
}

// Reconcile compares every city the cache knows with the database; it fails if any cannot be read.
func (c *Cache) Reconcile(ctx context.Context) error {
	c.mu.Lock()
	cities := make([]string, 0, len(c.known))
	for city := range c.known {
		cities = append(cities, city)
	}
	c.mu.Unlock()
	for _, city := range cities {
		revision, err := c.loader.Revision(ctx, city)
		if err != nil {
			reconciles.WithLabelValues("error").Inc()
			return fmt.Errorf("reconcile %s: %w", city, err)
		}
		c.Observe(city, revision)
		if s, ok := c.l1.Get(city); ok && s.Revision < revision {
			reconciles.WithLabelValues("behind").Inc()
			c.warm(city, revision)
			continue
		}
		reconciles.WithLabelValues("match").Inc()
	}
	return nil
}

// Announce takes a published revision: a city held in memory is rebuilt in the background.
func (c *Cache) Announce(m catalogevent.Invalidation) {
	invalidationLag.Observe(c.now().Sub(m.PublishedAt).Seconds())
	revision := domain.CatalogRevision(m.CatalogRevision)
	c.Observe(m.City, revision)
	if s, ok := c.l1.Get(m.City); ok && s.Revision < revision {
		c.warm(m.City, revision)
	}
}

func (c *Cache) warm(city string, revision domain.CatalogRevision) {
	if c.warmCtx.Err() != nil {
		return
	}
	c.warmGroup.Go(func() {
		if _, _, err := c.obtain(c.warmCtx, city, revision); err != nil && c.warmCtx.Err() == nil {
			c.log.Warn("warm catalog slice", zap.String("city", city), zap.Error(err))
		}
	})
}

// waitWarm lets tests wait for background builds.
func (c *Cache) waitWarm() {
	c.warmGroup.Wait()
}
