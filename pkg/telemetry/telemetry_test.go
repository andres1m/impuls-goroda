package telemetry

import (
	"context"
	"slices"
	"testing"

	"github.com/andres1m/impuls-goroda/pkg/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestInitWithoutEndpoint(t *testing.T) {
	tr := New("svc", config.Telemetry{}, zap.NewNop())
	if err := tr.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tr.provider != nil {
		t.Fatal("exporter must stay off without an endpoint")
	}
	if fields := otel.GetTextMapPropagator().Fields(); !slices.Contains(fields, "traceparent") {
		t.Fatalf("propagator fields = %v", fields)
	}
	if err := tr.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestInitRejectsBadRatio(t *testing.T) {
	for _, ratio := range []float64{-0.1, 1.5} {
		tr := New("svc", config.Telemetry{OTLPEndpoint: "127.0.0.1:4317", SampleRatio: ratio}, zap.NewNop())
		if err := tr.Init(context.Background()); err == nil {
			t.Fatalf("ratio %v accepted", ratio)
		}
	}
}

// The exporter connects lazily, so an unreachable collector must not fail startup.
func TestInitWithUnreachableEndpoint(t *testing.T) {
	tr := New("svc", config.Telemetry{OTLPEndpoint: "127.0.0.1:1"}, zap.NewNop())
	if err := tr.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, span := otel.Tracer("test").Start(context.Background(), "op")
	span.End()
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	_ = tr.Stop(ctx)
}

func TestLogFields(t *testing.T) {
	if got := LogFields(context.Background()); len(got) != 0 {
		t.Fatalf("empty context gave %v", got)
	}
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled})
	ctx := WithRequestID(trace.ContextWithSpanContext(context.Background(), sc), "req-1")
	enc := zapcore.NewMapObjectEncoder()
	for _, f := range LogFields(ctx) {
		f.AddTo(enc)
	}
	if enc.Fields["trace_id"] != sc.TraceID().String() || enc.Fields["span_id"] != sc.SpanID().String() || enc.Fields["request_id"] != "req-1" {
		t.Fatalf("fields = %v", enc.Fields)
	}
	if len(TraceFields(ctx)) != 2 {
		t.Fatal("trace fields must not carry the request id")
	}
}
