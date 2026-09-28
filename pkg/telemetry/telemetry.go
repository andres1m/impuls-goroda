package telemetry

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"

	"github.com/andres1m/impuls-goroda/pkg/config"
	"github.com/andres1m/impuls-goroda/pkg/svc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/zap"
)

// Tracing installs the process-wide trace context propagation and, with an endpoint, OTLP export.
type Tracing struct {
	service  string
	cfg      config.Telemetry
	log      *zap.Logger
	provider *sdktrace.TracerProvider
}

func New(service string, cfg config.Telemetry, log *zap.Logger) *Tracing {
	if log == nil {
		log = zap.NewNop()
	}
	return &Tracing{service: service, cfg: cfg, log: log}
}

func (t *Tracing) Name() string        { return "telemetry" }
func (t *Tracing) DependsOn() []string { return []string{"logger"} }

func (t *Tracing) Init(ctx context.Context) error {
	if t.provider != nil {
		return errors.New("telemetry already initialized")
	}
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	if t.cfg.OTLPEndpoint == "" {
		t.log.Info("trace export disabled")
		return nil
	}
	ratio := t.cfg.SampleRatio
	if ratio < 0 || ratio > 1 {
		return fmt.Errorf("trace sample ratio %v is outside 0..1", ratio)
	}
	if ratio == 0 {
		ratio = 1
	}
	exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(t.cfg.OTLPEndpoint), otlptracegrpc.WithInsecure())
	if err != nil {
		return fmt.Errorf("create trace exporter: %w", err)
	}
	t.provider = sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.NewSchemaless(
			attribute.String("service.name", t.service),
			attribute.String("service.version", version()),
		)),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))),
	)
	otel.SetTracerProvider(t.provider)
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		t.log.Warn("trace export", zap.Error(err))
	}))
	t.log.Info("trace export enabled", zap.String("endpoint", t.cfg.OTLPEndpoint), zap.Float64("sample_ratio", ratio))
	return nil
}

func (t *Tracing) HealthCheck(context.Context) error { return nil }
func (t *Tracing) Run(context.Context) error         { return nil }

// Stop flushes the spans still in the batch before the process exits.
func (t *Tracing) Stop(ctx context.Context) error {
	if t.provider == nil {
		return nil
	}
	if err := t.provider.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown tracer provider: %w", err)
	}
	return nil
}

func version() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				return s.Value
			}
		}
	}
	return "dev"
}

var _ svc.Service = (*Tracing)(nil)
