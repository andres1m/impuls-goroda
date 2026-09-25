package temporal

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/andres1m/impuls-goroda/pkg/config"
	"github.com/andres1m/impuls-goroda/pkg/svc"
	"go.temporal.io/sdk/worker"
	"go.uber.org/zap"
)

type Worker struct {
	client         *Client
	cfg            config.Temporal
	register       func(worker.Registry)
	TemporalWorker worker.Worker
	mu             sync.Mutex
	started        bool
	stopOnce       sync.Once
	stopped        chan struct{}
	fatalErrors    chan error
}

func NewWorker(_ *zap.Logger, c *Client, cfg *config.Temporal, register func(worker.Registry)) (*Worker, error) {
	if c == nil || cfg == nil || cfg.QueueName == "" || cfg.WorkerCount <= 0 || register == nil {
		return nil, errors.New("temporal worker requires a client, queue, positive concurrency and registration callback")
	}
	return &Worker{client: c, cfg: *cfg, register: register, stopped: make(chan struct{}), fatalErrors: make(chan error, 1)}, nil
}
func (w *Worker) Name() string        { return "temporal-worker" }
func (w *Worker) DependsOn() []string { return []string{"logger", "temporal-client"} }
func (w *Worker) Init(context.Context) error {
	if w.TemporalWorker != nil {
		return errors.New("temporal worker already initialized")
	}
	if w.client.TemporalClient == nil {
		return errors.New("temporal client is not initialized")
	}
	w.TemporalWorker = worker.New(w.client.TemporalClient, w.cfg.QueueName, worker.Options{
		MaxConcurrentActivityExecutionSize:     w.cfg.WorkerCount,
		MaxConcurrentWorkflowTaskExecutionSize: max(2, w.cfg.WorkerCount),
		WorkerStopTimeout:                      5 * time.Second,
		OnFatalError: func(err error) {
			select {
			case w.fatalErrors <- err:
			default:
			}
		},
	})
	w.register(w.TemporalWorker)
	return nil
}

// HealthCheck checks initialization; server connectivity is checked by the client.
func (w *Worker) HealthCheck(context.Context) error {
	if w.TemporalWorker == nil {
		return errors.New("temporal worker is not initialized")
	}
	return nil
}
func (w *Worker) Run(ctx context.Context) error {
	w.mu.Lock()
	if err := ctx.Err(); err != nil {
		w.mu.Unlock()
		return err
	}
	if w.TemporalWorker == nil {
		w.mu.Unlock()
		return errors.New("temporal worker is not initialized")
	}
	if w.started {
		w.mu.Unlock()
		return errors.New("temporal worker already started")
	}
	if err := w.TemporalWorker.Start(); err != nil {
		w.mu.Unlock()
		return err
	}
	w.started = true
	w.mu.Unlock()
	select {
	case err := <-w.fatalErrors:
		return err
	case <-ctx.Done():
		return nil
	}
}

func (w *Worker) Stop(ctx context.Context) error {
	w.stopOnce.Do(func() {
		go func() {
			w.mu.Lock()
			defer w.mu.Unlock()
			defer close(w.stopped)
			if w.started {
				w.TemporalWorker.Stop()
				w.started = false
			}
		}()
	})
	select {
	case <-w.stopped:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var _ svc.Service = (*Worker)(nil)
