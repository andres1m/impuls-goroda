package main

import (
	"testing"

	"github.com/andres1m/impuls-goroda/pkg/config"
)

func TestStandConfigLoads(t *testing.T) {
	t.Setenv("SYNCER_DB_PASSWORD", "x")
	t.Setenv("REDIS_PASSWORD", "x")
	t.Setenv("OPENROUTER_API_KEY", "x")
	var cfg appConfig
	if err := config.Load("../../../../docker/syncer/config.yaml", &cfg); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Kafka.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Kafka.RawTopic != "integration.raw" || cfg.Kafka.ConsumerGroup != "syncer-raw" || cfg.Kafka.RawPartitions != 3 {
		t.Fatalf("kafka = %+v", cfg.Kafka)
	}
}
