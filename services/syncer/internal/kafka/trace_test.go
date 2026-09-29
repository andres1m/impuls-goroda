package kafka

import (
	"context"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/plugin/kotel"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
)

type spanStarter struct{ seen trace.SpanContext }

func (s *spanStarter) Start(ctx context.Context, _ *ingest.Envelope) (bool, error) {
	s.seen = trace.SpanContextFromContext(ctx)
	return false, nil
}

func tracedConsumer(t *testing.T, starter Starter) *Consumer {
	t.Helper()
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider())
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTracerProvider(previous) })
	return NewConsumer(zap.NewNop(), testConfig("unused:9092"), starter, &fakeDeadLetters{})
}

func TestHandleRecordWithoutTrace(t *testing.T) {
	starter := &spanStarter{}
	consumer := tracedConsumer(t, starter)
	record := &kgo.Record{Topic: "integration.raw", Value: envelopeBytes(t)}
	if err := consumer.handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if !starter.seen.IsValid() {
		t.Fatal("workflow start ran outside a span")
	}
}

func TestHandleRecordContinuesTrace(t *testing.T) {
	starter := &spanStarter{}
	consumer := tracedConsumer(t, starter)
	producer, span := otel.Tracer("test").Start(context.Background(), "publish")
	span.End()
	record := &kgo.Record{Topic: "integration.raw", Value: envelopeBytes(t)}
	otel.GetTextMapPropagator().Inject(producer, kotel.NewRecordCarrier(record))
	// The client's fetch hook does this for records it polls.
	consumer.tracer.OnFetchRecordBuffered(record)

	if err := consumer.handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if starter.seen.TraceID() != span.SpanContext().TraceID() {
		t.Fatalf("start in trace %s; record came from %s", starter.seen.TraceID(), span.SpanContext().TraceID())
	}
}
