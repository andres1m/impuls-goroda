package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/plugin/kotel"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
)

// franz-go retries an unreachable broker until the context ends, so publishing needs its own deadline.
const publishTimeout = 30 * time.Second

type Producer struct {
	client     *kgo.Client
	cfg        Config
	timeout    time.Duration
	topicReady bool
}

func NewProducer(cfg Config) (*Producer, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.DefaultProduceTopic(cfg.RawTopic),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.WithHooks(kotel.NewKotel(kotel.WithTracer(kotel.NewTracer())).Hooks()...),
	)
	if err != nil {
		return nil, fmt.Errorf("kafka producer: %w", err)
	}
	return &Producer{client: client, cfg: cfg, timeout: publishTimeout}, nil
}

func (p *Producer) Publish(ctx context.Context, envelopes []ingest.Envelope) error {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	if !p.topicReady {
		if err := EnsureTopic(ctx, p.client, p.cfg.RawTopic, p.cfg.RawPartitions); err != nil {
			return err
		}
		p.topicReady = true
	}
	records := make([]*kgo.Record, 0, len(envelopes))
	for i := range envelopes {
		e := &envelopes[i]
		value, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("encode envelope %s: %w", e.RawIngestID, err)
		}
		records = append(records, &kgo.Record{Key: []byte(e.Key()), Value: value})
	}
	if err := p.client.ProduceSync(ctx, records...).FirstErr(); err != nil {
		return fmt.Errorf("produce to %s: %w", p.cfg.RawTopic, err)
	}
	return nil
}

func (p *Producer) Close() {
	p.client.Close()
}

// EnsureTopic creates the topic with the broker's default replication; an existing topic is kept as is.
func EnsureTopic(ctx context.Context, client *kgo.Client, topic string, partitions int32) error {
	responses, err := kadm.NewClient(client).CreateTopics(ctx, partitions, -1, nil, topic)
	if err == nil {
		err = responses[topic].Err
	}
	if err != nil && !errors.Is(err, kerr.TopicAlreadyExists) {
		return fmt.Errorf("create topic %s: %w", topic, err)
	}
	return nil
}
