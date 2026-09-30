// Package schedule collects live sources on a timetable.
package schedule

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"

	"github.com/andres1m/impuls-goroda/pkg/svc"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
)

var runs = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "syncer_scheduled_ingest_total",
	Help: "Scheduled collection runs by source, city and result.",
}, []string{"source", "city", "result"})

const (
	tickInterval = time.Minute
	minEvery     = time.Minute
)

// scheduled lists the sources that are polled: a prepared export is read once, not repeatedly.
var scheduled = map[domain.SourceKey]bool{domain.KudaGo: true, domain.OSM: true}

type JobConfig struct {
	Source string        `yaml:"source"`
	City   string        `yaml:"city"`
	Every  time.Duration `yaml:"every"`
}

// Config lists what to collect and how often. No job is scheduled unless the operator lists it: polling
// intervals depend on what each source allows.
type Config struct {
	Jobs []JobConfig `yaml:"jobs"`
}

type Job struct {
	Source domain.SourceKey
	City   domain.City
	Every  time.Duration
}

func (c Config) Parse() ([]Job, error) {
	jobs := make([]Job, 0, len(c.Jobs))
	seen := make(map[[2]string]bool, len(c.Jobs))
	for _, j := range c.Jobs {
		source := domain.SourceKey(j.Source)
		if !scheduled[source] {
			return nil, fmt.Errorf("schedule: source %q cannot be polled", j.Source)
		}
		city, err := domain.ParseCity(j.City)
		if err != nil {
			return nil, fmt.Errorf("schedule: %w", err)
		}
		if j.Every < minEvery {
			return nil, fmt.Errorf("schedule: %s %s needs an interval of at least %s", j.Source, j.City, minEvery)
		}
		key := [2]string{j.Source, j.City}
		if seen[key] {
			return nil, fmt.Errorf("schedule: %s %s is listed twice", j.Source, j.City)
		}
		seen[key] = true
		jobs = append(jobs, Job{Source: source, City: city, Every: j.Every})
	}
	return jobs, nil
}

type Ingester interface {
	Ingest(ctx context.Context, adapter ingest.Adapter, city domain.City) (ingest.Result, error)
}

type Store interface {
	LastAttempt(ctx context.Context, source domain.SourceKey, city domain.City) (at time.Time, known bool, err error)
	TryLock(ctx context.Context, source domain.SourceKey, city domain.City) (release func(), locked bool, err error)
}

type Scheduler struct {
	jobs     []Job
	adapters map[domain.SourceKey]ingest.Adapter
	ingester Ingester
	store    Store
	log      *zap.Logger
	now      func() time.Time
	tick     time.Duration

	mu      sync.Mutex
	stopped bool
	running chan struct{}
}

func New(
	jobs []Job,
	adapters map[domain.SourceKey]ingest.Adapter,
	ingester Ingester,
	store Store,
	log *zap.Logger,
) (*Scheduler, error) {
	for _, j := range jobs {
		if adapters[j.Source] == nil {
			return nil, fmt.Errorf("schedule: no adapter for %s", j.Source)
		}
	}
	return &Scheduler{
		jobs: jobs, adapters: adapters, ingester: ingester, store: store, log: log, now: time.Now, tick: tickInterval,
	}, nil
}

func (s *Scheduler) Name() string                      { return "ingest-scheduler" }
func (s *Scheduler) DependsOn() []string               { return []string{"logger", "db"} }
func (s *Scheduler) Init(context.Context) error        { return nil }
func (s *Scheduler) HealthCheck(context.Context) error { return nil }

// Stop waits for the collection in progress, so that the database it writes to is not closed under it.
func (s *Scheduler) Stop(ctx context.Context) error {
	s.mu.Lock()
	s.stopped = true
	running := s.running
	s.mu.Unlock()
	if running == nil {
		return nil
	}
	select {
	case <-running:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stop ingest scheduler: %w", ctx.Err())
	}
}

func (s *Scheduler) Run(ctx context.Context) error {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil
	}
	running := make(chan struct{})
	s.running = running
	s.mu.Unlock()
	defer close(running)

	ticker := time.NewTicker(s.tick)
	defer ticker.Stop()
	for {
		s.RunDue(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunDue collects every listed source whose last attempt is older than its interval. A failed attempt
// counts as an attempt, so a source that is down is tried again after one interval, not at every tick.
func (s *Scheduler) RunDue(ctx context.Context) {
	for _, job := range s.jobs {
		if ctx.Err() != nil {
			return
		}
		s.runIfDue(ctx, job)
	}
}

func (s *Scheduler) runIfDue(ctx context.Context, job Job) {
	fields := []zap.Field{zap.String("source", string(job.Source)), zap.String("city", string(job.City))}
	release, locked, err := s.store.TryLock(ctx, job.Source, job.City)
	if err != nil {
		s.log.Warn("lock scheduled ingest", append(fields, zap.Error(err))...)
		return
	}
	if !locked {
		return
	}
	defer release()
	last, known, err := s.store.LastAttempt(ctx, job.Source, job.City)
	if err != nil {
		s.log.Warn("read last attempt", append(fields, zap.Error(err))...)
		return
	}
	if known && s.now().Sub(last) < job.Every {
		return
	}
	result, err := s.ingester.Ingest(ctx, s.adapters[job.Source], job.City)
	if err != nil {
		runs.WithLabelValues(string(job.Source), string(job.City), "failed").Inc()
		s.log.Warn("scheduled ingest failed", append(fields, zap.Error(err))...)
		return
	}
	runs.WithLabelValues(string(job.Source), string(job.City), "succeeded").Inc()
	s.log.Info("scheduled ingest", append(fields,
		zap.Int("received", result.Received), zap.Int("inserted", result.Inserted),
		zap.Int("published", result.Published), zap.Int("skipped", result.Skipped))...)
}

var _ svc.Service = (*Scheduler)(nil)
