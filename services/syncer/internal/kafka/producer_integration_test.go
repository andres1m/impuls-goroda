package kafka

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
)

func kafkaBrokers(t *testing.T) []string {
	t.Helper()
	brokers := os.Getenv("SYNCER_TEST_KAFKA_BROKERS")
	if brokers == "" {
		t.Skip("SYNCER_TEST_KAFKA_BROKERS is not set")
	}
	return strings.Split(brokers, ",")
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

func TestProducerIntegration(t *testing.T) {
	cfg := testConfig(kafkaBrokers(t)...)
	cfg.RawTopic = "integration.raw.test-" + randomSuffix(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	producer, err := NewProducer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	first := testEnvelope("0b5c3f6e-2d1a-4c8e-9f3b-7a6d5e4c3b2a")
	second := testEnvelope("1c6d4f7e-3e2b-4d9f-8a4c-8b7e6f5d4c3b")
	if err := producer.Publish(ctx, []ingest.Envelope{first}); err != nil {
		t.Fatal(err)
	}
	if err := producer.Publish(ctx, []ingest.Envelope{second}); err != nil {
		t.Fatal(err)
	}

	reader, err := kgo.NewClient(kgo.SeedBrokers(cfg.Brokers...), kgo.ConsumeTopics(cfg.RawTopic), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err := EnsureTopic(ctx, reader, cfg.RawTopic, cfg.RawPartitions); err != nil {
		t.Fatalf("second EnsureTopic must be a no-op: %v", err)
	}
	got := map[string]ingest.Envelope{}
	for len(got) < 2 {
		fetches := reader.PollFetches(ctx)
		if err := ctx.Err(); err != nil {
			t.Fatalf("read %d of 2 records: %v", len(got), err)
		}
		for _, record := range fetches.Records() {
			if string(record.Key) != "moscow:event:1" {
				t.Fatalf("key = %q", record.Key)
			}
			envelope, err := ingest.DecodeEnvelope(record.Value)
			if err != nil {
				t.Fatal(err)
			}
			got[envelope.RawIngestID] = envelope
		}
	}
	if got[first.RawIngestID] != first || got[second.RawIngestID] != second {
		t.Fatalf("got %+v", got)
	}
}
