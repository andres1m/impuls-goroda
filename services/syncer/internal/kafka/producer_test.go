package kafka

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
)

func testEnvelope(rawIngestID string) ingest.Envelope {
	return ingest.Envelope{
		Version:       ingest.EnvelopeVersion,
		RawIngestID:   rawIngestID,
		Source:        domain.KudaGo,
		City:          domain.Moscow,
		ExternalID:    "event:1",
		ContentHash:   "ab12",
		FetchedAt:     time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC),
		DataMode:      domain.Live,
		SchemaVersion: "kudago-api-1.4",
	}
}

func testConfig(brokers ...string) Config {
	return Config{Brokers: brokers, RawTopic: "integration.raw", RawPartitions: 3, ConsumerGroup: "syncer-raw"}
}

func TestConfigValidate(t *testing.T) {
	if err := testConfig("kafka:9092").Validate(); err != nil {
		t.Fatal(err)
	}
	broken := []Config{
		testConfig(),
		{Brokers: []string{"kafka:9092"}, RawPartitions: 3, ConsumerGroup: "g"},
		{Brokers: []string{"kafka:9092"}, RawTopic: "t", ConsumerGroup: "g"},
		{Brokers: []string{"kafka:9092"}, RawTopic: "t", RawPartitions: 3},
	}
	for _, cfg := range broken {
		if err := cfg.Validate(); err == nil {
			t.Errorf("accepted %+v", cfg)
		}
	}
}

// silentBroker accepts connections and never answers, like a broker behind a dead network path.
func silentBroker(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	t.Cleanup(func() {
		listener.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
	}()
	return listener.Addr().String()
}

func TestPublishGivesUpOnSilentBroker(t *testing.T) {
	producer, err := NewProducer(testConfig(silentBroker(t)))
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	producer.timeout = 2 * time.Second

	started := time.Now()
	err = producer.Publish(
		context.Background(),
		[]ingest.Envelope{testEnvelope("0b5c3f6e-2d1a-4c8e-9f3b-7a6d5e4c3b2a")},
	)
	if err == nil {
		t.Fatal("publish to a silent broker succeeded")
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("publish took %s", elapsed)
	}
}
