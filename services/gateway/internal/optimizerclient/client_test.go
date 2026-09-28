package optimizerclient

import (
	"context"
	"testing"

	"github.com/andres1m/impuls-goroda/pkg/config"
)

func TestClientStartsWithoutOptimizerServer(t *testing.T) {
	client := New(nil, config.GRPCClient{Host: "127.0.0.1", Port: 1})
	if client.RPC() != nil {
		t.Fatal("RPC client available before initialization")
	}
	if err := client.HealthCheck(context.Background()); err == nil {
		t.Fatal("uninitialized client reported healthy")
	}
	if err := client.Init(context.Background()); err != nil {
		t.Fatalf("initialize virtual connection: %v", err)
	}
	t.Cleanup(func() { _ = client.Stop(context.Background()) })
	if client.RPC() == nil {
		t.Fatal("RPC client unavailable after initialization")
	}
	if err := client.HealthCheck(context.Background()); err != nil {
		t.Fatalf("local health check required a running server: %v", err)
	}
	if err := client.Stop(context.Background()); err != nil {
		t.Fatalf("stop client: %v", err)
	}
	if client.RPC() != nil {
		t.Fatal("RPC client remains available after stop")
	}
	if err := client.HealthCheck(context.Background()); err == nil {
		t.Fatal("stopped client reported healthy")
	}
}
