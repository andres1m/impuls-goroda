package rpc

import (
	"context"
	"time"

	"github.com/andres1m/impuls-goroda/pkg/telemetry"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	requestIDHeader   = "x-request-id"
	maxRequestIDRunes = 128
)

var (
	serverDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "grpc_server_request_duration_seconds",
		Help: "Unary gRPC call duration on the server by method and status code.",
	}, []string{"method", "code"})
	clientDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "grpc_client_request_duration_seconds",
		Help: "Unary gRPC call duration on the client by method and status code.",
	}, []string{"method", "code"})
)

func serverTelemetry(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	ctx = telemetry.WithRequestID(ctx, incomingRequestID(ctx))
	start := time.Now()
	resp, err := handler(ctx, req)
	serverDuration.WithLabelValues(info.FullMethod, status.Code(err).String()).Observe(time.Since(start).Seconds())
	return resp, err
}

func clientTelemetry(
	ctx context.Context,
	method string,
	req, reply any,
	cc *grpc.ClientConn,
	invoker grpc.UnaryInvoker,
	opts ...grpc.CallOption,
) error {
	if id := telemetry.RequestID(ctx); id != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, requestIDHeader, id)
	}
	start := time.Now()
	err := invoker(ctx, method, req, reply, cc, opts...)
	clientDuration.WithLabelValues(method, status.Code(err).String()).Observe(time.Since(start).Seconds())
	return err
}

// incomingRequestID bounds a caller-chosen value before it reaches every log line of the call.
func incomingRequestID(ctx context.Context) string {
	values := metadata.ValueFromIncomingContext(ctx, requestIDHeader)
	if len(values) == 0 {
		return ""
	}
	id := []rune(values[0])
	if len(id) > maxRequestIDRunes {
		id = id[:maxRequestIDRunes]
	}
	return string(id)
}
