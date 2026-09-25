package redis

import (
	"context"
	"testing"

	"github.com/andres1m/impuls-goroda/pkg/config"
	r "github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

func TestClusterWithSingleSeed(t *testing.T) {
	c, err := NewRedis(zap.NewNop(), config.Redis{Mode: "cluster", Addresses: []string{"127.0.0.1:6379"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer c.Stop(context.Background())
	if _, ok := c.Pool.(*r.ClusterClient); !ok {
		t.Fatalf("client = %T", c.Pool)
	}
}
func TestInvalidConfig(t *testing.T) {
	for _, cfg := range []config.Redis{
		{Mode: "cluster", Addresses: []string{"localhost:6379"}, DB: 1},
		{Mode: "standalone", Addresses: []string{"a:1", "b:2"}},
		{Mode: "unknown", Addresses: []string{"a:1"}}, {},
	} {
		if _, err := NewRedis(zap.NewNop(), cfg); err == nil {
			t.Fatalf("accepted %+v", cfg)
		}
	}
}
