package main

import (
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/pkg/config"
)

func TestStandConfigLoads(t *testing.T) {
	t.Setenv("OPTIMIZER_DB_PASSWORD", "x")
	t.Setenv("REDIS_PASSWORD", "x")
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("POLZA_API_KEY", "")
	var cfg appConfig
	if err := config.Load("../../../../docker/optimizer/config.yaml", &cfg); err != nil {
		t.Fatal(err)
	}
	c := cfg.CatalogCache
	if c.L1MaxBytes != 256<<20 || c.L2TTL != 24*time.Hour || c.SessionHorizon != 24*time.Hour ||
		c.HealthInterval != 5*time.Second || c.ReconcileInterval != time.Minute {
		t.Fatalf("catalog cache = %+v", c)
	}
}
