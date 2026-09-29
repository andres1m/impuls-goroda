package main

import (
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/pkg/config"
)

func TestStandConfigLoads(t *testing.T) {
	t.Setenv("SYNCER_DB_PASSWORD", "x")
	t.Setenv("REDIS_PASSWORD", "x")
	t.Setenv("OPENROUTER_API_KEY", "x")
	t.Setenv("POLZA_API_KEY", "")
	var cfg appConfig
	if err := config.Load("../../../../docker/syncer/config.yaml", &cfg); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Kafka.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Delivery.PollInterval != 500*time.Millisecond || cfg.Delivery.Batch != 100 ||
		cfg.Delivery.BackoffMax != time.Minute {
		t.Fatalf("delivery = %+v", cfg.Delivery)
	}
	if cfg.Kafka.RawTopic != "integration.raw" || cfg.Kafka.ConsumerGroup != "syncer-raw" ||
		cfg.Kafka.RawPartitions != 3 || cfg.Kafka.DLQTopic != "dlq.integration.raw" {
		t.Fatalf("kafka = %+v", cfg.Kafka)
	}
	if cfg.Kafka.UrgentTopic != "events.lifecycle.urgent" {
		t.Fatalf("urgent topic = %q", cfg.Kafka.UrgentTopic)
	}
	if cfg.Lifecycle.Host != "gateway" || cfg.Lifecycle.Port != 50052 || cfg.Lifecycle.UseTLS {
		t.Fatalf("lifecycle = %+v", cfg.Lifecycle)
	}
}
