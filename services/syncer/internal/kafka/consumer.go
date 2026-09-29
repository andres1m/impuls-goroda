package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/plugin/kotel"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/andres1m/impuls-goroda/pkg/svc"
	"github.com/andres1m/impuls-goroda/pkg/telemetry"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
)

var (
	rawWorkflows = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "syncer_raw_workflows_total",
		Help: "Raw ingest workflow starts by outcome.",
	}, []string{"result"})
)

const (
	maxRetryShift = 5
	maxRetryDelay = 30 * time.Second
)

type Starter interface {
	Start(ctx context.Context, envelope *ingest.Envelope) (existed bool, err error)
}

type groupClient interface {
	Ping(ctx context.Context) error
	PollFetches(ctx context.Context) kgo.Fetches
	CommitRecords(ctx context.Context, records ...*kgo.Record) error
	AllowRebalance()
	CloseAllowingRebalance()
}

type Consumer struct {
	log        *zap.Logger
	cfg        Config
	starter    Starter
	client     groupClient
	tracer     *kotel.Tracer
	retryDelay func(attempt int) time.Duration
}

func NewConsumer(log *zap.Logger, cfg Config, starter Starter) *Consumer {
	return &Consumer{
		log:        log,
		cfg:        cfg,
		starter:    starter,
		tracer:     kotel.NewTracer(kotel.ConsumerGroup(cfg.ConsumerGroup)),
		retryDelay: startRetryDelay,
	}
}

func startRetryDelay(attempt int) time.Duration {
	return min(time.Second<<min(attempt, maxRetryShift), maxRetryDelay)
}

func (c *Consumer) Name() string        { return "kafka-consumer" }
func (c *Consumer) DependsOn() []string { return []string{"logger", "temporal-client"} }

func (c *Consumer) Init(ctx context.Context) error {
	if err := c.cfg.Validate(); err != nil {
		return err
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(c.cfg.Brokers...),
		kgo.ConsumerGroup(c.cfg.ConsumerGroup),
		kgo.ConsumeTopics(c.cfg.RawTopic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.DisableAutoCommit(),
		kgo.BlockRebalanceOnPoll(),
		kgo.WithHooks(kotel.NewKotel(kotel.WithTracer(c.tracer)).Hooks()...),
	)
	if err != nil {
		return fmt.Errorf("kafka consumer: %w", err)
	}
	c.client = client
	return EnsureTopic(ctx, client, c.cfg.RawTopic, c.cfg.RawPartitions)
}

func (c *Consumer) HealthCheck(ctx context.Context) error {
	if c.client == nil {
		return errors.New("kafka consumer is not initialized")
	}
	return c.client.Ping(ctx)
}

// Run commits a poll only after every record in it has a workflow, so a crash in between
// redelivers the records and the workflow ID absorbs the repeat.
func (c *Consumer) Run(ctx context.Context) error {
	for c.pollOnce(ctx) {
	}
	return nil
}

// pollOnce reports whether to keep polling. Every poll, even one ended by cancellation,
// blocks rebalances until released, and leaving the group on close waits for that.
func (c *Consumer) pollOnce(ctx context.Context) bool {
	fetches := c.client.PollFetches(ctx)
	defer c.client.AllowRebalance()
	if ctx.Err() != nil || fetches.IsClientClosed() {
		return false
	}
	for _, fetchErr := range fetches.Errors() {
		c.log.Warn(
			"kafka fetch",
			zap.String("topic", fetchErr.Topic),
			zap.Int32("partition", fetchErr.Partition),
			zap.Error(fetchErr.Err),
		)
	}
	records := fetches.Records()
	for _, record := range records {
		if err := c.handle(ctx, record); err != nil {
			return false
		}
	}
	if len(records) > 0 {
		if err := c.client.CommitRecords(ctx, records...); err != nil {
			c.log.Warn("commit raw offsets", zap.Error(err))
		}
	}
	return true
}

func (c *Consumer) Stop(ctx context.Context) error {
	if c.client == nil {
		return nil
	}
	// A plain Close would wait forever to leave the group: polling blocks rebalances until allowed.
	closed := make(chan struct{})
	go func() {
		c.client.CloseAllowingRebalance()
		close(closed)
	}()
	select {
	case <-closed:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stop kafka consumer: %w", ctx.Err())
	}
}

// handle continues the trace the producer put into the record's headers, so the workflow it
// starts joins the ingest run that published the record.
func (c *Consumer) handle(ctx context.Context, record *kgo.Record) error {
	recordCtx, span := c.tracer.WithProcessSpan(record)
	defer span.End()
	ctx = trace.ContextWithSpan(ctx, trace.SpanFromContext(recordCtx))
	envelope, err := ingest.DecodeEnvelope(record.Value)
	if err != nil {
		// ponytail: an invalid envelope is counted and dropped; it goes to the dead letter topic once that exists.
		ingest.SchemaMismatch.WithLabelValues(sourceLabel(record.Value)).Inc()
		c.log.Error(
			"invalid raw envelope",
			append(
				[]zap.Field{
					zap.Int32("partition", record.Partition),
					zap.Int64("offset", record.Offset),
					zap.Error(err),
				},
				telemetry.TraceFields(ctx)...)...)
		return nil
	}
	for attempt := 0; ; attempt++ {
		existed, startErr := c.starter.Start(ctx, &envelope)
		if startErr == nil {
			rawWorkflows.WithLabelValues(startResult(existed)).Inc()
			return nil
		}
		c.log.Warn(
			"start raw ingest workflow",
			append(
				[]zap.Field{
					zap.String("raw_ingest_id", envelope.RawIngestID),
					zap.Int("attempt", attempt),
					zap.Error(startErr),
				},
				telemetry.TraceFields(ctx)...)...)
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait to retry workflow start: %w", ctx.Err())
		case <-time.After(c.retryDelay(attempt)):
		}
	}
}

func startResult(existed bool) string {
	if existed {
		return "existing"
	}
	return "started"
}

// sourceLabel keeps the metric's label set bounded whatever a broken envelope contains.
func sourceLabel(value []byte) string {
	var probe struct {
		Source domain.SourceKey `json:"source"`
	}
	if json.Unmarshal(value, &probe) != nil {
		return "unknown"
	}
	switch probe.Source {
	case domain.MkrfEvents, domain.KudaGo, domain.OSM, domain.SyntheticSource:
		return string(probe.Source)
	}
	return "unknown"
}

var _ svc.Service = (*Consumer)(nil)
