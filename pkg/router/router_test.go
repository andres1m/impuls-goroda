package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"
)

func TestRouteRegistersMethodPathAndMiddleware(t *testing.T) {
	e := echo.New()
	var middlewareCalled bool
	mark := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			middlewareCalled = true
			return next(c)
		}
	}
	handler := func() echo.HandlerFunc {
		return func(c *echo.Context) error { return c.String(http.StatusTeapot, "ok") }
	}

	NewRoute(http.MethodPost, "/items", handler, mark).Register(context.Background(), e.Group("/api"))

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/items", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTeapot)
	}
	if !middlewareCalled {
		t.Fatal("route middleware was not called")
	}

	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/items", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}
