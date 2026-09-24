package temporal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/pkg/config"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"go.uber.org/zap"
)

func TestClientPreservesOptionsAndHandlesUninitialized(t *testing.T) {
	c := NewClient(zap.NewNop(), &client.Options{Identity: "test-worker"}, &config.Temporal{HostPort: "localhost:7233", Namespace: "default"})
	if c.temporalConf.Identity != "test-worker" {
		t.Fatal("SDK options discarded")
	}
	if err := c.HealthCheck(context.Background()); err == nil {
		t.Fatal("uninitialized client healthy")
	}
	if err := c.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Init(ctx); err == nil {
		t.Fatal("ignored canceled dial")
	}
}
func TestWorkerRegistersWithoutDomainDependencies(t *testing.T) {
	lazy, err := client.NewLazyClient(client.Options{HostPort: "localhost:7233"})
	if err != nil {
		t.Fatal(err)
	}
	defer lazy.Close()
	c := &Client{TemporalClient: lazy}
	called := false
	w, err := NewWorker(zap.NewNop(), c, &config.Temporal{QueueName: "test", WorkerCount: 1}, func(r worker.Registry) {
		called = true
		r.RegisterWorkflow(func(workflow.Context) error { return nil })
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("registration callback not called")
	}
	if err := w.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type stubWorker struct {
	worker.Worker
	start func() error
}

func (w *stubWorker) Start() error { return w.start() }
func (w *stubWorker) Stop()        {}
func TestWorkerReportsFatalError(t *testing.T) {
	w, err := NewWorker(zap.NewNop(), &Client{}, &config.Temporal{QueueName: "test", WorkerCount: 1}, func(worker.Registry) {})
	if err != nil {
		t.Fatal(err)
	}
	fatal := errors.New("worker failed")
	w.TemporalWorker = &stubWorker{start: func() error { w.fatalErrors <- fatal; return nil }}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := w.Run(ctx); !errors.Is(err, fatal) {
		t.Fatalf("Run error = %v", err)
	}
	if err := w.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}
