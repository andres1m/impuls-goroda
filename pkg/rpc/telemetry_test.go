package rpc

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/andres1m/impuls-goroda/pkg/config"
	"github.com/andres1m/impuls-goroda/pkg/telemetry"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
)

const checkMethod = "/grpc.health.v1.Health/Check"

type seen struct {
	requestID string
	traceID   trace.TraceID
}

// startPair serves the health service and returns a client connected to it; seen records what
// the server's handler context carried.
func startPair(t *testing.T) (healthpb.HealthClient, *seen) {
	t.Helper()
	var got seen
	capture := func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		got = seen{requestID: telemetry.RequestID(ctx), traceID: trace.SpanContextFromContext(ctx).TraceID()}
		return handler(ctx, req)
	}
	server := NewServer("test", nil, &config.GRPCServer{}, WithUnaryInterceptors(capture))
	server.OnInit(func(s *Server) { healthpb.RegisterHealthServer(s.GetServer(), health.NewServer()) })
	ctx := context.Background()
	if err := server.Init(ctx); err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Run(ctx) }()
	t.Cleanup(func() { _ = server.Stop(context.Background()) })

	client := NewClient("test", nil, &config.GRPCClient{Host: "127.0.0.1", Port: server.Addr().(*net.TCPAddr).Port})
	if err := client.Init(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Stop(context.Background()) })
	return healthpb.NewHealthClient(client.GetConn()), &got
}

func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTracerProvider(previous) })
	return recorder
}

func observations(t *testing.T, vec *prometheus.HistogramVec, labels ...string) uint64 {
	t.Helper()
	var m dto.Metric
	if err := vec.WithLabelValues(labels...).(prometheus.Metric).Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetHistogram().GetSampleCount()
}

func TestTraceAndRequestIDCrossTheCall(t *testing.T) {
	recorder := recordSpans(t)
	client, got := startPair(t)
	serverBefore := observations(t, serverDuration, checkMethod, "OK")
	clientBefore := observations(t, clientDuration, checkMethod, "OK")

	ctx, parent := otel.Tracer("test").Start(telemetry.WithRequestID(context.Background(), "req-42"), "parent")
	if _, err := client.Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatal(err)
	}
	parent.End()

	traceID := parent.SpanContext().TraceID()
	if got.requestID != "req-42" || got.traceID != traceID {
		t.Fatalf("server saw %+v; want req-42 in trace %s", *got, traceID)
	}
	kinds := map[trace.SpanKind]bool{}
	for _, span := range recorder.Ended() {
		if span.SpanContext().TraceID() == traceID {
			kinds[span.SpanKind()] = true
		}
	}
	if !kinds[trace.SpanKindClient] || !kinds[trace.SpanKindServer] {
		t.Fatalf("span kinds in the trace: %v", kinds)
	}
	if observations(t, serverDuration, checkMethod, "OK") != serverBefore+1 ||
		observations(t, clientDuration, checkMethod, "OK") != clientBefore+1 {
		t.Fatal("call not measured on both sides")
	}
}

func TestRequestIDTrimmed(t *testing.T) {
	recordSpans(t)
	client, got := startPair(t)
	ctx := metadata.AppendToOutgoingContext(context.Background(), requestIDHeader, strings.Repeat("a", 300))
	if _, err := client.Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatal(err)
	}
	if len(got.requestID) != maxRequestIDRunes {
		t.Fatalf("request id length %d", len(got.requestID))
	}
}
