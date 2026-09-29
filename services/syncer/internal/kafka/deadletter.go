package kafka

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/plugin/kotel"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/delivery"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
)

var deadLetterWrites = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "syncer_dead_letters_total",
	Help: "Records written to the dead letter topic, by the stage that rejected them.",
}, []string{"stage"})

// deadLetterTimeout stays well below the outbox lease: a send outliving the lease cannot record its
// failure, and the row returns at once instead of after a backoff.
const deadLetterTimeout = 10 * time.Second

type producerClient interface {
	ProduceSync(ctx context.Context, records ...*kgo.Record) kgo.ProduceResults
}

// DeadLetters writes to the dead letter topic for the raw consumer and the outbox relay alike.
type DeadLetters struct {
	cfg     Config
	client  producerClient
	close   func()
	timeout time.Duration
	ensure  func(ctx context.Context) error

	mu         sync.Mutex
	topicReady bool
}

// NewDeadLetters does not reach the broker: the topic is created with the first letter.
func NewDeadLetters(cfg *Config) (*DeadLetters, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.WithHooks(kotel.NewKotel(kotel.WithTracer(kotel.NewTracer())).Hooks()...),
	)
	if err != nil {
		return nil, fmt.Errorf("kafka dead letters: %w", err)
	}
	return &DeadLetters{
		cfg:     *cfg,
		client:  client,
		close:   client.Close,
		timeout: deadLetterTimeout,
		ensure:  func(ctx context.Context) error { return EnsureTopic(ctx, client, cfg.DLQTopic, cfg.RawPartitions) },
	}, nil
}

func (d *DeadLetters) Close() {
	if d.close != nil {
		d.close()
	}
}

// Write returns once the broker holds the letter, so the caller may let go of its own copy.
func (d *DeadLetters) Write(ctx context.Context, key []byte, letter *ingest.DeadLetter) error {
	value, err := ingest.EncodeDeadLetter(letter)
	if err != nil {
		return fmt.Errorf("dead letter: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	if err := d.topic(ctx); err != nil {
		return err
	}
	record := &kgo.Record{Topic: d.cfg.DLQTopic, Key: key, Value: value}
	if err := d.client.ProduceSync(ctx, record).FirstErr(); err != nil {
		return fmt.Errorf("produce to %s: %w", d.cfg.DLQTopic, err)
	}
	deadLetterWrites.WithLabelValues(string(letter.Stage)).Inc()
	return nil
}

func (d *DeadLetters) topic(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.topicReady {
		return nil
	}
	if err := d.ensure(ctx); err != nil {
		return err
	}
	d.topicReady = true
	return nil
}

// Send delivers a dead letter the outbox recorded with a materialized batch.
func (d *DeadLetters) Send(ctx context.Context, item *delivery.Item) error {
	if item.EventType != ingest.DeadLetterEventType {
		return fmt.Errorf("kafka destination has no topic for %q", item.EventType)
	}
	letter, err := ingest.DecodeDeadLetter(item.Payload)
	if err != nil {
		return fmt.Errorf("outbox dead letter %s: %w", item.ID, err)
	}
	return d.Write(ctx, letter.Key(), &letter)
}

var _ delivery.Sender = (*DeadLetters)(nil)
