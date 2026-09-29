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
	"github.com/andres1m/impuls-goroda/services/syncer/internal/lifecycle"
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

// DeadLetters writes to the dead letter and urgent topics for the raw consumer and the outbox relay alike.
type DeadLetters struct {
	cfg     Config
	client  producerClient
	close   func()
	timeout time.Duration
	ensure  func(ctx context.Context, topic string) error

	mu    sync.Mutex
	ready map[string]bool
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
		ensure: func(ctx context.Context, topic string) error {
			return EnsureTopic(ctx, client, topic, cfg.RawPartitions)
		},
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
	if err := d.produce(ctx, d.cfg.DLQTopic, key, value); err != nil {
		return err
	}
	deadLetterWrites.WithLabelValues(string(letter.Stage)).Inc()
	return nil
}

func (d *DeadLetters) produce(ctx context.Context, topic string, key, value []byte) error {
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	if err := d.topic(ctx, topic); err != nil {
		return err
	}
	record := &kgo.Record{Topic: topic, Key: key, Value: value}
	if err := d.client.ProduceSync(ctx, record).FirstErr(); err != nil {
		return fmt.Errorf("produce to %s: %w", topic, err)
	}
	return nil
}

func (d *DeadLetters) topic(ctx context.Context, name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ready[name] {
		return nil
	}
	if err := d.ensure(ctx, name); err != nil {
		return err
	}
	if d.ready == nil {
		d.ready = make(map[string]bool)
	}
	d.ready[name] = true
	return nil
}

// Send delivers what the outbox recorded for the kafka destination: a dead letter of a materialized batch
// or a session cancellation.
func (d *DeadLetters) Send(ctx context.Context, item *delivery.Item) error {
	switch item.EventType {
	case ingest.DeadLetterEventType:
		letter, err := ingest.DecodeDeadLetter(item.Payload)
		if err != nil {
			return fmt.Errorf("outbox dead letter %s: %w", item.ID, err)
		}
		return d.Write(ctx, letter.Key(), &letter)
	case lifecycle.KafkaEventType:
		c, err := lifecycle.Decode(item.Payload)
		if err != nil {
			return fmt.Errorf("outbox cancellation %s: %w", item.ID, err)
		}
		return d.produce(ctx, d.cfg.UrgentTopic, c.Key(), item.Payload)
	default:
		return fmt.Errorf("kafka destination has no topic for %q", item.EventType)
	}
}

var _ delivery.Sender = (*DeadLetters)(nil)
