package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

type requestIDKey struct{}

func WithRequestID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDKey{}, id)
}

func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// TraceFields ties a log line to its trace in Jaeger.
func TraceFields(ctx context.Context) []zap.Field {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return nil
	}
	return []zap.Field{zap.String("trace_id", sc.TraceID().String()), zap.String("span_id", sc.SpanID().String())}
}

func LogFields(ctx context.Context) []zap.Field {
	fields := TraceFields(ctx)
	if id := RequestID(ctx); id != "" {
		fields = append(fields, zap.String("request_id", id))
	}
	return fields
}
