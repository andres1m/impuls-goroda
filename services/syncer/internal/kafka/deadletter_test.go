package kafka

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/delivery"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
)

type fakeProducer struct {
	records []*kgo.Record
	err     error
}

func (p *fakeProducer) ProduceSync(_ context.Context, records ...*kgo.Record) kgo.ProduceResults {
	results := make(kgo.ProduceResults, len(records))
	for i, r := range records {
		p.records = append(p.records, r)
		results[i] = kgo.ProduceResult{Record: r, Err: p.err}
	}
	return results
}

func testDeadLetters(p *fakeProducer, ensured *int) *DeadLetters {
	return &DeadLetters{cfg: testConfig("unused:9092"), client: p, timeout: publishTimeout,
		ensure: func(context.Context) error { *ensured++; return nil }}
}

func payloadLetter() ingest.DeadLetter {
	return ingest.DeadLetter{Version: ingest.DeadLetterVersion, Stage: ingest.StagePayload,
		Reason: domain.InvalidSchema, RawIngestID: "0b5c3f6e-2d1a-4c8e-9f3b-7a6d5e4c3b2a", Source: domain.OSM,
		City: domain.Perm, ExternalID: "node/2", Error: "bad_payload"}
}

func TestDeadLettersWriteCreatesTheTopicOnce(t *testing.T) {
	producer, ensured := &fakeProducer{}, 0
	d := testDeadLetters(producer, &ensured)
	letter := payloadLetter()
	for range 2 {
		if err := d.Write(context.Background(), []byte("key"), &letter); err != nil {
			t.Fatal(err)
		}
	}
	if ensured != 1 || len(producer.records) != 2 || producer.records[0].Topic != "dlq.integration.raw" ||
		string(producer.records[0].Key) != "key" {
		t.Fatalf("ensured %d, records %+v", ensured, producer.records)
	}
	if got, err := ingest.DecodeDeadLetter(
		producer.records[0].Value,
	); err != nil ||
		got.RawIngestID != letter.RawIngestID {
		t.Fatalf("value %+v, %v", got, err)
	}
}

func TestDeadLettersSendDeliversAnOutboxLetter(t *testing.T) {
	producer, ensured := &fakeProducer{}, 0
	letter := payloadLetter()
	payload, err := ingest.EncodeDeadLetter(&letter)
	if err != nil {
		t.Fatal(err)
	}
	item := &delivery.Item{
		Destination: ingest.DeadLetterDestination,
		EventType:   ingest.DeadLetterEventType,
		Payload:     payload,
	}
	if err := testDeadLetters(producer, &ensured).Send(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	if len(producer.records) != 1 || string(producer.records[0].Key) != "perm:node/2" {
		t.Fatalf("records %+v", producer.records)
	}
}

func TestDeadLettersSendRefusesOtherEvents(t *testing.T) {
	producer, ensured := &fakeProducer{}, 0
	item := &delivery.Item{
		Destination: ingest.DeadLetterDestination,
		EventType:   "events.lifecycle.urgent",
		Payload:     []byte(`{}`),
	}
	if err := testDeadLetters(
		producer,
		&ensured,
	).Send(context.Background(), item); err == nil ||
		len(producer.records) != 0 {
		t.Fatalf("err %v, records %d", err, len(producer.records))
	}
}

func TestDeadLettersReportBrokerErrors(t *testing.T) {
	producer, ensured := &fakeProducer{err: errors.New("not enough replicas")}, 0
	letter := payloadLetter()
	if err := testDeadLetters(producer, &ensured).Write(context.Background(), nil, &letter); err == nil {
		t.Fatal("broker error lost")
	}
}

// A send that outlives the relay's 30 s lease loses its failure mark, and the row comes back at once
// instead of after a backoff.
func TestDeadLettersGiveUpWellWithinTheOutboxLease(t *testing.T) {
	cfg := testConfig("unused:9092")
	d, err := NewDeadLetters(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.timeout > 10*time.Second {
		t.Fatalf("timeout %v", d.timeout)
	}
}
