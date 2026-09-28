package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

var requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name: "http_server_request_duration_seconds",
	Help: "HTTP request duration by server, method, route template and status code.",
}, []string{"server", "method", "route", "code"})

// instrument runs outermost, so it sees the status the error handler finally wrote and the
// request logger inside it sees the span. Spans and labels use the route template: paths carry
// share tokens, and raw paths would also let scanners grow the label set without bound.
func instrument(server string) echo.MiddlewareFunc {
	tracer := otel.Tracer("github.com/andres1m/impuls-goroda/pkg/server")
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			route := c.Path()
			if route == "/healthz" || route == "/metrics" {
				return next(c)
			}
			if route == "" {
				route = "unmatched"
			}
			req := c.Request()
			method := metricMethod(req.Method)
			ctx := otel.GetTextMapPropagator().Extract(req.Context(), propagation.HeaderCarrier(req.Header))
			ctx, span := tracer.Start(ctx, method+" "+route,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(attribute.String("http.request.method", method), attribute.String("http.route", route)))
			defer span.End()
			c.SetRequest(req.WithContext(ctx))

			start := time.Now()
			err := next(c)
			_, status := echo.ResolveResponseStatus(c.Response(), err)
			span.SetAttributes(attribute.Int("http.response.status_code", status))
			if status >= http.StatusInternalServerError {
				span.SetStatus(codes.Error, http.StatusText(status))
			}
			requestDuration.WithLabelValues(server, method, route, strconv.Itoa(status)).Observe(time.Since(start).Seconds())
			return err
		}
	}
}

func metricMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace, http.MethodPatch:
		return method
	default:
		return "_OTHER"
	}
}
