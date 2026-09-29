package server

import (
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andres1m/impuls-goroda/pkg/config"
	"github.com/labstack/echo/v5"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTracerProvider(previous) })
	return recorder
}

func newTestServer(name string, opts ...Option) *Server {
	s := New(name, config.HTTPServer{}, append(opts, WithHealth())...)
	s.api.GET("/api/v1/items/:id", func(c *echo.Context) error { return c.String(http.StatusOK, "ok") })
	s.api.GET("/api/v1/shared-routes/:share_token", func(c *echo.Context) error { return c.NoContent(http.StatusOK) })
	s.api.GET("/conflict", func(*echo.Context) error { return echo.NewHTTPError(http.StatusConflict, "stale") })
	s.api.GET("/broken", func(*echo.Context) error { return errors.New("boom") })
	return s
}

func serve(s *Server, path string, header http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	maps.Copy(req.Header, header)
	rec := httptest.NewRecorder()
	s.api.ServeHTTP(rec, req)
	return rec
}

func observations(t *testing.T, labels ...string) uint64 {
	t.Helper()
	var m dto.Metric
	if err := requestDuration.WithLabelValues(labels...).(prometheus.Metric).Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetHistogram().GetSampleCount()
}

func TestSpanFromIncomingTraceparent(t *testing.T) {
	recorder := recordSpans(t)
	s := newTestServer("span-test")
	const traceID = "0102030405060708090a0b0c0d0e0f10"
	serve(s, "/api/v1/items/7", http.Header{"Traceparent": {"00-" + traceID + "-0a0b0c0d0e0f1011-01"}})

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans = %d", len(spans))
	}
	span := spans[0]
	if span.Name() != "GET /api/v1/items/:id" || span.SpanContext().TraceID().String() != traceID {
		t.Fatalf("span %q in trace %s", span.Name(), span.SpanContext().TraceID())
	}
	attrs := map[string]string{}
	for _, a := range span.Attributes() {
		attrs[string(a.Key)] = a.Value.String()
	}
	if attrs["http.route"] != "/api/v1/items/:id" || attrs["http.response.status_code"] != "200" {
		t.Fatalf("attributes = %v", attrs)
	}
	if observations(t, "span-test", http.MethodGet, "/api/v1/items/:id", "200") != 1 {
		t.Fatal("request not measured")
	}
}

func TestErrorStatusMatchesWire(t *testing.T) {
	recorder := recordSpans(t)
	s := newTestServer("status-test", WithLogger(zap.NewNop()))

	if rec := serve(s, "/conflict", nil); rec.Code != http.StatusConflict {
		t.Fatalf("wire status %d", rec.Code)
	}
	if rec := serve(s, "/broken", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("wire status %d", rec.Code)
	}
	spans := recorder.Ended()
	if len(spans) != 2 {
		t.Fatalf("spans = %d", len(spans))
	}
	if spans[0].Status().Code == codes.Error || spans[1].Status().Code != codes.Error {
		t.Fatalf("statuses %v, %v", spans[0].Status(), spans[1].Status())
	}
	if observations(t, "status-test", http.MethodGet, "/conflict", "409") != 1 ||
		observations(t, "status-test", http.MethodGet, "/broken", "500") != 1 {
		t.Fatal("status codes not measured as sent")
	}
}

func TestUnmatchedRoute(t *testing.T) {
	recordSpans(t)
	s := newTestServer("unmatched-test")
	serve(s, "/no/such/path", nil)
	serve(s, "/another/random/path", nil)
	if observations(t, "unmatched-test", http.MethodGet, "unmatched", "404") != 2 {
		t.Fatal("unknown paths are not folded into one series")
	}
}

func TestLogUsesRouteNotURI(t *testing.T) {
	recordSpans(t)
	core, logs := observer.New(zap.InfoLevel)
	s := newTestServer("log-test", WithLogger(zap.New(core)))
	serve(s, "/api/v1/shared-routes/secret-token?x=1", nil)

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("log entries = %d", len(entries))
	}
	fields := entries[0].ContextMap()
	for key, value := range fields {
		if s, ok := value.(string); ok && strings.Contains(s, "secret-token") {
			t.Fatalf("field %s leaks the token: %q", key, s)
		}
	}
	if fields["route"] != "/api/v1/shared-routes/:share_token" || fields["trace_id"] == nil {
		t.Fatalf("fields = %v", fields)
	}
}

func TestOpsPathsNotTraced(t *testing.T) {
	recorder := recordSpans(t)
	s := newTestServer("ops-test")
	serve(s, "/healthz", nil)
	if len(recorder.Ended()) != 0 {
		t.Fatal("health probe traced")
	}
}

func TestUnknownMethodsUseOneSeries(t *testing.T) {
	recorder := recordSpans(t)
	s := newTestServer("method-test")
	for _, method := range []string{"CUSTOMA", "CUSTOMB", "CUSTOMC"} {
		response := httptest.NewRecorder()
		s.api.ServeHTTP(response, httptest.NewRequest(method, "/unknown", http.NoBody))
		if response.Code != http.StatusNotFound {
			t.Fatalf("status = %d", response.Code)
		}
	}
	if got := observations(t, "method-test", "_OTHER", "unmatched", "404"); got != 3 {
		t.Fatalf("unknown method observations = %d, want 3", got)
	}
	for _, span := range recorder.Ended() {
		if span.Name() != "_OTHER unmatched" {
			t.Fatalf("unbounded span name: %q", span.Name())
		}
		for _, attr := range span.Attributes() {
			if attr.Key == "http.request.method" && attr.Value.AsString() != "_OTHER" {
				t.Fatalf("unbounded method: %v", attr)
			}
		}
	}
}
