// Package delivery sends the consequences of catalog publications recorded in the outbox after the
// publishing transaction commits, retrying each until its destination accepts it.
package delivery

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"

	"github.com/andres1m/impuls-goroda/pkg/svc"
)

var (
	deliveries = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "syncer_change_delivery_total",
		Help: "Outbox deliveries by destination and result.",
	}, []string{"destination", "result"})
	backlog = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "syncer_change_delivery_backlog",
		Help: "Outbox rows not delivered yet.",
	})
	oldest = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "syncer_change_delivery_oldest_seconds",
		Help: "Age of the oldest outbox row not delivered yet.",
	})
)

type Item struct {
	ID          uuid.UUID
	Destination string
	EventType   string
	Payload     []byte
	// Including the delivery in progress.
	Attempts  int
	CreatedAt time.Time
	// The lease the row was claimed under, as stored.
	LeaseUntil time.Time
}

type Store interface {
	// Claim leases due rows of the destinations until leaseUntil; rows another relay holds are skipped.
	Claim(ctx context.Context, now, leaseUntil time.Time, destinations []string, limit int) ([]Item, error)
	// Delivered and Failed mark the row only while it is still under the item's lease.
	Delivered(ctx context.Context, item Item, at time.Time) error
	Failed(ctx context.Context, item Item, next time.Time) error
	Backlog(ctx context.Context, now time.Time) (int, time.Duration, error)
}

type Sender interface {
	Send(ctx context.Context, item Item) error
}

type Config struct {
	PollInterval time.Duration `yaml:"poll-interval"`
	Batch        int           `yaml:"batch"`
	// A relay that dies mid-delivery leaves the row to others once the lease ends.
	Lease      time.Duration `yaml:"lease"`
	BackoffMin time.Duration `yaml:"backoff-min"`
	BackoffMax time.Duration `yaml:"backoff-max"`
}

func (c Config) withDefaults() Config {
	if c.PollInterval == 0 {
		c.PollInterval = 500 * time.Millisecond
	}
	if c.Batch == 0 {
		c.Batch = 100
	}
	if c.Lease == 0 {
		c.Lease = 30 * time.Second
	}
	if c.BackoffMin == 0 {
		c.BackoffMin = time.Second
	}
	if c.BackoffMax == 0 {
		c.BackoffMax = time.Minute
	}
	return c
}

func (c Config) validate() error {
	if c.PollInterval <= 0 || c.Batch <= 0 || c.Lease <= 0 || c.BackoffMin <= 0 || c.BackoffMax < c.BackoffMin {
		return errors.New("delivery config needs positive intervals, batch and lease, and backoff-min not above backoff-max")
	}
	return nil
}

type Relay struct {
	cfg          Config
	store        Store
	senders      map[string]Sender
	destinations []string
	log          *zap.Logger
	now          func() time.Time

	mu      sync.Mutex
	stopped bool
	// Closed when Run returns.
	running chan struct{}
}

type Option func(*Relay)

func WithClock(now func() time.Time) Option {
	return func(r *Relay) { r.now = now }
}

func NewRelay(cfg Config, store Store, senders map[string]Sender, log *zap.Logger, opts ...Option) (*Relay, error) {
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if len(senders) == 0 {
		return nil, errors.New("delivery relay needs at least one destination")
	}
	for destination, s := range senders {
		if s == nil {
			return nil, fmt.Errorf("destination %q has no sender", destination)
		}
	}
	r := &Relay{
		cfg: cfg, store: store, senders: senders, log: log, now: time.Now,
		destinations: slices.Sorted(maps.Keys(senders)),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r, nil
}

func (r *Relay) Name() string                      { return "delivery-relay" }
func (r *Relay) DependsOn() []string               { return []string{"logger", "db", "redis"} }
func (r *Relay) Init(context.Context) error        { return nil }
func (r *Relay) HealthCheck(context.Context) error { return nil }

// Stop waits for the delivery in progress, so that the database it marks is not closed under it.
func (r *Relay) Stop(ctx context.Context) error {
	r.mu.Lock()
	r.stopped = true
	running := r.running
	r.mu.Unlock()
	if running == nil {
		return nil
	}
	select {
	case <-running:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Run never gives up on a failing database or destination: the outbox keeps the work until they return.
// An outage is reported once, when it starts.
func (r *Relay) Run(ctx context.Context) error {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return nil
	}
	running := make(chan struct{})
	r.running = running
	r.mu.Unlock()
	defer close(running)

	ticker := time.NewTicker(r.cfg.PollInterval)
	defer ticker.Stop()
	failing := false
	for {
		err := r.Tick(ctx)
		switch {
		case ctx.Err() != nil:
		case err != nil && !failing:
			failing = true
			r.log.Warn("deliver outbox", zap.Error(err))
		case err != nil:
			r.log.Debug("deliver outbox", zap.Error(err))
		case failing:
			failing = false
			r.log.Info("outbox delivery recovered")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Tick delivers every row that is due now.
func (r *Relay) Tick(ctx context.Context) error {
	for {
		now := r.now()
		items, err := r.store.Claim(ctx, now, now.Add(r.cfg.Lease), r.destinations, r.cfg.Batch)
		if err != nil {
			return fmt.Errorf("claim outbox rows: %w", err)
		}
		for _, item := range items {
			r.deliver(ctx, item)
		}
		if len(items) < r.cfg.Batch {
			break
		}
	}
	count, age, err := r.store.Backlog(ctx, r.now())
	if err != nil {
		return fmt.Errorf("measure outbox backlog: %w", err)
	}
	backlog.Set(float64(count))
	oldest.Set(age.Seconds())
	return nil
}

// deliver leaves a row whose mark cannot be written to the lease, so it is sent again later;
// the receivers treat a repeat as a no-op.
func (r *Relay) deliver(ctx context.Context, item Item) {
	err := r.senders[item.Destination].Send(ctx, item)
	if err == nil {
		deliveries.WithLabelValues(item.Destination, "delivered").Inc()
		if err := r.store.Delivered(ctx, item, r.now()); err != nil {
			r.log.Warn("mark outbox row delivered", zap.Stringer("id", item.ID), zap.Error(err))
		}
		return
	}
	deliveries.WithLabelValues(item.Destination, "failed").Inc()
	r.log.Warn("outbox delivery failed", zap.Stringer("id", item.ID), zap.String("destination", item.Destination),
		zap.Int("attempts", item.Attempts), zap.Error(err))
	next := r.now().Add(Backoff(item.Attempts, r.cfg.BackoffMin, r.cfg.BackoffMax))
	if err := r.store.Failed(ctx, item, next); err != nil {
		r.log.Warn("mark outbox row failed", zap.Stringer("id", item.ID), zap.Error(err))
	}
}

// Backoff doubles the pause after every failed attempt, from minimum up to maximum.
func Backoff(attempts int, minimum, maximum time.Duration) time.Duration {
	pause := minimum
	for i := 1; i < attempts && pause < maximum; i++ {
		pause *= 2
	}
	return min(pause, maximum)
}

var _ svc.Service = (*Relay)(nil)
