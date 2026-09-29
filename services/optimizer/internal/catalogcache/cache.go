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
const (
	buildTimeout          = 30 * time.Second
	defaultL1MaxBytes     = 256 << 20
	defaultL2TTL          = 24 * time.Hour
	defaultSessionHorizon = 24 * time.Hour
	defaultHealthInterval = 5 * time.Second
	l1NumCounters         = 1000
	l1BufferItems         = 64
)

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
		c.L1MaxBytes = defaultL1MaxBytes
	}
	if c.L2TTL == 0 {
		c.L2TTL = defaultL2TTL
	}
	if c.SessionHorizon == 0 {
		c.SessionHorizon = defaultSessionHorizon
	}
	if c.HealthInterval == 0 {
		c.HealthInterval = defaultHealthInterval
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
	// Writes to memory hold it for reading, so none runs into a closed cache.
	closing sync.RWMutex
	closed  bool
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
		NumCounters: l1NumCounters, MaxCost: cfg.L1MaxBytes, BufferItems: l1BufferItems, IgnoreInternalCost: true,
	})
	if err != nil {
		return nil, fmt.Errorf("create catalog slice cache: %w", err)
	}
	c := &Cache{
		cfg:    cfg,
		loader: loader,
		l2:     l2,
		log:    log,
		now:    time.Now,
		l1:     l1,
		known:  map[string]domain.CatalogRevision{},
	}
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
		c.closing.Lock()
		defer c.closing.Unlock()
		c.closed = true
		c.l1.Close()
	})
}

// write changes memory unless the cache is closed.
func (c *Cache) write(change func()) {
	c.closing.RLock()
	defer c.closing.RUnlock()
	if !c.closed {
		change()
	}
}

// Candidates expands the city's slice for the request.
func (c *Cache) Candidates(
	ctx context.Context,
	req *domain.OptimizeRequest,
) ([]domain.Candidate, domain.DataFreshness, error) {
	if err := req.Validate(); err != nil {
		return nil, domain.DataFreshness{}, fmt.Errorf("%w: %w", usecase.ErrInvalidRequest, err)
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
	return c.build(ctx, city, revision)
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

// build gets the slice of at least the revision from the shared store or the database. Requests for
// the same revision share one build; a build for an older one is not joined, since it may have read
// the catalog before the revision was published.
func (c *Cache) build(
	ctx context.Context,
	city string,
	revision domain.CatalogRevision,
) (*catalogslice.Slice, string, error) {
	ch := c.builds.DoChan(fmt.Sprintf("%s:%d", city, revision), func() (any, error) {
		if s, ok := c.l1.Get(city); ok && s.Revision >= revision {
			return obtained{s, levelChecked}, nil
		}
		buildCtx, cancel := context.WithTimeout(c.warmCtx, buildTimeout)
		defer cancel()
		if s, ok, err := c.l2.Get(buildCtx, city, revision); err != nil {
			l2Errors.WithLabelValues("get").Inc()
			c.log.Warn("read catalog slice from the shared cache", zap.String("city", city), zap.Error(err))
		} else if ok {
			c.keep(s)
			return obtained{s, levelL2}, nil
		}
		started := time.Now()
		s, err := c.loader.LoadSlice(buildCtx, city, c.now().Add(-c.cfg.SessionHorizon))
		if err != nil {
			return nil, err
		}
		buildSeconds.Observe(time.Since(started).Seconds())
		c.keep(s)
		if err := c.l2.Put(buildCtx, s); err != nil {
			l2Errors.WithLabelValues("put").Inc()
			c.log.Warn("write catalog slice to the shared cache", zap.String("city", city), zap.Error(err))
		}
		return obtained{s, levelDB}, nil
	})
	select {
	case <-ctx.Done():
		return nil, "", fmt.Errorf("wait catalog slice build: %w", ctx.Err())
	case r := <-ch:
		if r.Err != nil {
			return nil, "", r.Err
		}
		o, ok := r.Val.(obtained)
		if !ok {
			return nil, "", errors.New("unexpected singleflight result")
		}
		return o.slice, o.level, nil
	}
}

// keep puts the slice in memory unless a newer one is there already.
func (c *Cache) keep(s *catalogslice.Slice) {
	c.Observe(s.City, s.Revision)
	c.write(func() {
		if cur, ok := c.l1.Get(s.City); ok && cur.Revision >= s.Revision {
			return
		}
		c.l1.Set(s.City, s, s.Size())
		c.l1.Wait()
		sliceRevision.WithLabelValues(s.City).Set(float64(s.Revision))
	})
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
// A city the catalog no longer has is forgotten.
func (c *Cache) Reconcile(ctx context.Context) error {
	c.mu.Lock()
	cities := make([]string, 0, len(c.known))
	for city := range c.known {
		cities = append(cities, city)
	}
	c.mu.Unlock()
	for _, city := range cities {
		revision, err := c.loader.Revision(ctx, city)
		if errors.Is(err, usecase.ErrCatalogNotReady) {
			reconciles.WithLabelValues("gone").Inc()
			c.forget(city)
			continue
		}
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

func (c *Cache) forget(city string) {
	c.mu.Lock()
	delete(c.known, city)
	c.mu.Unlock()
	c.write(func() {
		c.l1.Del(city)
		c.l1.Wait()
		sliceRevision.DeleteLabelValues(city)
	})
}

// Announce takes a published revision: a city held in memory is rebuilt in the background.
func (c *Cache) Announce(m *catalogevent.Invalidation) {
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
		if _, _, err := c.build(c.warmCtx, city, revision); err != nil && c.warmCtx.Err() == nil {
			c.log.Warn("warm catalog slice", zap.String("city", city), zap.Error(err))
		}
	})
}

// waitWarm lets tests wait for background builds.
func (c *Cache) waitWarm() {
	c.warmGroup.Wait()
}
