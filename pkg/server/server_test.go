package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/pkg/config"
	"github.com/andres1m/impuls-goroda/pkg/router"
	"github.com/labstack/echo/v5"
)

type testRouter struct{ routes []router.Route }

func (r testRouter) Routes() []router.Route { return r.routes }

func pingRouter() router.Router {
	return testRouter{routes: []router.Route{
		router.NewRoute(http.MethodGet, "/ping", func() echo.HandlerFunc {
			return func(c *echo.Context) error { return c.String(http.StatusOK, "pong") }
		}),
	}}
}

func startServer(t *testing.T, opts ...Option) (*Server, string) {
	t.Helper()
	s := New("test-http", config.HTTPServer{Port: 0, ReadTimeout: time.Second}, opts...)
	ctx := context.Background()
	if err := s.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.HealthCheck(ctx); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	runErr := make(chan error, 1)
	go func() { runErr <- s.Run(ctx) }()
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.Stop(stopCtx); err != nil {
			t.Errorf("Stop: %v", err)
		}
		if err := <-runErr; err != nil {
			t.Errorf("Run: %v", err)
		}
	})
	return s, "http://" + s.Addr().String()
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestRouterIsMountedUnderAPIVersion(t *testing.T) {
	_, base := startServer(t, WithRouter(context.Background(), pingRouter()))

	if code, body := get(t, base+"/api/v1/ping"); code != http.StatusOK || body != "pong" {
		t.Fatalf("got %d %q", code, body)
	}
	if code, _ := get(t, base+"/ping"); code != http.StatusNotFound {
		t.Fatalf("unprefixed route status = %d, want 404", code)
	}
}

func TestRouterGroupAddsPrefix(t *testing.T) {
	_, base := startServer(t, WithRouterGroup(context.Background(), "/routes", pingRouter()))

	if code, _ := get(t, base+"/api/v1/routes/ping"); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
}

func TestHealthWithoutMetrics(t *testing.T) {
	_, base := startServer(t, WithHealth())

	if code, _ := get(t, base+"/healthz"); code != http.StatusOK {
		t.Fatalf("healthz status = %d", code)
	}
	if code, _ := get(t, base+"/metrics"); code != http.StatusNotFound {
		t.Fatalf("metrics exposed without WithMetrics: status %d", code)
	}
}

func TestOpsEndpoints(t *testing.T) {
	_, base := startServer(t, WithHealth(), WithMetrics())

	code, body := get(t, base+"/healthz")
	if code != http.StatusOK || !strings.Contains(body, `"status":"ok"`) {
		t.Fatalf("healthz: %d %q", code, body)
	}
	code, body = get(t, base+"/metrics")
	if code != http.StatusOK || !strings.Contains(body, "go_goroutines") {
		t.Fatalf("metrics: %d, go_goroutines present = %v", code, strings.Contains(body, "go_goroutines"))
	}
}

func TestMiddlewareIsApplied(t *testing.T) {
	header := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			c.Response().Header().Set("X-Test", "1")
			return next(c)
		}
	}
	_, base := startServer(t, WithMiddleware(header), WithHealth())

	resp, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("X-Test") != "1" {
		t.Fatal("middleware header missing")
	}
}

func TestCustomHTTPErrorHandlerHandlesErrorsAndRecoveredPanics(t *testing.T) {
	routes := testRouter{routes: []router.Route{
		router.NewRoute(http.MethodGet, "/error", func() echo.HandlerFunc {
			return func(*echo.Context) error { return errors.New("handler failed") }
		}),
		router.NewRoute(http.MethodGet, "/panic", func() echo.HandlerFunc {
			return func(*echo.Context) error { panic("handler panicked") }
		}),
	}}
	handler := func(c *echo.Context, _ error) {
		_ = c.String(http.StatusTeapot, "safe error")
	}
	_, base := startServer(t,
		WithRouter(context.Background(), routes),
		WithHTTPErrorHandler(handler),
	)

	for _, path := range []string{"/error", "/panic"} {
		code, body := get(t, base+"/api/v1"+path)
		if code != http.StatusTeapot || body != "safe error" {
			t.Fatalf("%s: got %d %q", path, code, body)
		}
	}
}

func TestNilHTTPErrorHandlerKeepsDefault(t *testing.T) {
	routes := testRouter{routes: []router.Route{
		router.NewRoute(http.MethodGet, "/error", func() echo.HandlerFunc {
			return func(*echo.Context) error { return echo.ErrBadRequest }
		}),
	}}
	_, base := startServer(t,
		WithRouter(context.Background(), routes),
		WithHTTPErrorHandler(nil),
	)

	code, _ := get(t, base+"/api/v1/error")
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
}

func TestProbe(t *testing.T) {
	_, base := startServer(t, WithHealth())
	ctx := context.Background()

	if err := Probe(ctx, base+"/healthz"); err != nil {
		t.Fatalf("healthy probe: %v", err)
	}
	if err := Probe(ctx, base+"/missing"); err == nil {
		t.Fatal("probe of 404 succeeded")
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedAddr := lis.Addr().String()
	lis.Close()
	if err := Probe(ctx, fmt.Sprintf("http://%s/healthz", closedAddr)); err == nil {
		t.Fatal("probe of closed port succeeded")
	}
}

func TestProbeHonorsTimeout(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	go func() {
		conn, err := lis.Accept()
		if err == nil {
			defer conn.Close()
			time.Sleep(2 * time.Second)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := Probe(ctx, "http://"+lis.Addr().String()+"/healthz"); err == nil {
		t.Fatal("probe of hanging server succeeded")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("probe took %v, want it to honor the context deadline", elapsed)
	}
}

func TestNameAndDependencies(t *testing.T) {
	s := New("ops", config.HTTPServer{})
	if s.Name() != "ops" {
		t.Fatalf("Name = %q", s.Name())
	}
	if !slices.Equal(s.DependsOn(), []string{"logger"}) {
		t.Fatalf("default DependsOn = %v", s.DependsOn())
	}

	s = New("api", config.HTTPServer{}, WithDependsOn("logger", "db"))
	if !slices.Equal(s.DependsOn(), []string{"logger", "db"}) {
		t.Fatalf("DependsOn = %v", s.DependsOn())
	}
}

func TestStopBeforeRunReleasesPort(t *testing.T) {
	s := New("test-http", config.HTTPServer{Port: 0})
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop before Init: %v", err)
	}

	s = New("test-http", config.HTTPServer{Port: 0})
	if err := s.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	addr := s.Addr().String()
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop before Run: %v", err)
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("repeated Stop: %v", err)
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("port still held after Stop: %v", err)
	}
	lis.Close()
}

func TestInitRejectsInvalidPort(t *testing.T) {
	s := New("test-http", config.HTTPServer{Port: 70000})
	if err := s.Init(context.Background()); err == nil {
		t.Fatal("Init accepted port 70000")
	}
}
