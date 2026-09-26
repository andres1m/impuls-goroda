package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/auth"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

type fakeRuntime struct {
	issued         auth.IssuedSession
	exchangeErr    error
	pair           auth.SessionAccount
	authErr        error
	allowAnonymous bool
	allowUser      bool
	retryAfter     time.Duration
	ownerErr       error
	webhookOK      bool
}

func (f *fakeRuntime) ExchangeMax(context.Context, string) (auth.IssuedSession, error) {
	return f.issued, f.exchangeErr
}
func (f *fakeRuntime) AllowAnonymous(string, time.Time) (bool, time.Duration) {
	return f.allowAnonymous, f.retryAfter
}
func (f *fakeRuntime) Authenticate(context.Context, string) (auth.SessionAccount, error) {
	return f.pair, f.authErr
}
func (f *fakeRuntime) AllowAuthenticated(domain.UserID, time.Time) (bool, time.Duration) {
	return f.allowUser, f.retryAfter
}
func (f *fakeRuntime) RequireRouteOwner(context.Context, domain.RouteID, domain.UserID) error {
	return f.ownerErr
}
func (f *fakeRuntime) WebhookValid(string) bool { return f.webhookOK }

func testEcho() *echo.Echo {
	e := echo.New()
	e.Use(middleware.Recover(), RequestIDMiddleware)
	e.HTTPErrorHandler = ErrorHandler
	return e
}

func TestAuthRouteMatchesOpenAPIResponse(t *testing.T) {
	expiresAt := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	runtime := &fakeRuntime{
		allowAnonymous: true,
		issued: auth.IssuedSession{
			AccessToken: "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ",
			ExpiresAt:   expiresAt,
		},
	}
	e := testEcho()
	for _, route := range NewAuthRouter(runtime).Routes() {
		route.Register(context.Background(), e.Group("/api/v1"))
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/max", bytes.NewBufferString(`{"init_data":"signed"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set(echo.HeaderXRequestID, "request-1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response authResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.TokenType != "Bearer" || response.RequestID != "request-1" || response.ExpiresAt != expiresAt {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestAuthRouteRejectsMalformedInvalidAndLimitedRequests(t *testing.T) {
	tests := []struct {
		name    string
		runtime *fakeRuntime
		body    string
		status  int
		code    string
	}{
		{"malformed", &fakeRuntime{allowAnonymous: true}, `{"unknown":true}`, http.StatusBadRequest, "MALFORMED_REQUEST"},
		{"invalid auth", &fakeRuntime{allowAnonymous: true, exchangeErr: auth.ErrAuthRequired}, `{"init_data":"bad"}`, http.StatusUnauthorized, "AUTH_REQUIRED"},
		{"limited", &fakeRuntime{retryAfter: 2 * time.Second}, `{"init_data":"signed"}`, http.StatusTooManyRequests, "RATE_LIMITED"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := testEcho()
			for _, route := range NewAuthRouter(tc.runtime).Routes() {
				route.Register(context.Background(), e.Group("/api/v1"))
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/max", bytes.NewBufferString(tc.body)))
			if rec.Code != tc.status {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			var response errorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Code != tc.code || response.RequestID == "" {
				t.Fatalf("unexpected error: %+v", response)
			}
			if tc.status == http.StatusTooManyRequests && rec.Header().Get("Retry-After") != "2" {
				t.Fatalf("Retry-After=%q", rec.Header().Get("Retry-After"))
			}
		})
	}
}

func TestBearerMiddlewareAndAuthenticatedLimit(t *testing.T) {
	userID := domain.UserID{1}
	runtime := &fakeRuntime{
		allowUser: true,
		pair:      auth.SessionAccount{Account: domain.UserAccount{ID: userID, Kind: domain.AccountMax}},
	}
	e := testEcho()
	e.GET("/private", func(c *echo.Context) error {
		principal, ok := PrincipalFrom(c)
		if !ok || principal.UserID != userID {
			t.Fatal("principal missing")
		}
		return c.NoContent(http.StatusNoContent)
	}, Authenticate(runtime), AuthenticatedRateLimit(runtime))
	req := httptest.NewRequest(http.MethodGet, "/private", nil)
	req.Header.Set(echo.HeaderAuthorization, "Bearer abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/private", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing bearer status=%d", rec.Code)
	}
}

func TestSecurityMiddlewareAndOwnerGuard(t *testing.T) {
	runtime := &fakeRuntime{webhookOK: false, ownerErr: postgres.ErrNotFound}
	e := testEcho()
	e.POST("/webhook", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) }, VerifyWebhook(runtime))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/webhook", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("webhook status=%d", rec.Code)
	}
	var apiError *Error
	err := RequireOwner(context.Background(), runtime, domain.RouteID{1}, domain.UserID{2})
	if !errors.As(err, &apiError) || apiError.Status != http.StatusNotFound {
		t.Fatalf("owner error=%v", err)
	}
}

func TestCORSAndSafePanicResponse(t *testing.T) {
	cors, err := CORS("https://impuls-goroda.github.io")
	if err != nil {
		t.Fatal(err)
	}
	e := echo.New()
	e.Use(middleware.Recover(), RequestIDMiddleware, cors)
	e.HTTPErrorHandler = ErrorHandler
	e.GET("/panic", func(*echo.Context) error { panic("private failure") })

	allowed := httptest.NewRequest(http.MethodGet, "/panic", nil)
	allowed.Header.Set(echo.HeaderOrigin, "https://impuls-goroda.github.io")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, allowed)
	if rec.Code != http.StatusInternalServerError || rec.Header().Get(echo.HeaderAccessControlAllowOrigin) != "https://impuls-goroda.github.io" {
		t.Fatalf("status=%d origin=%q", rec.Code, rec.Header().Get(echo.HeaderAccessControlAllowOrigin))
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("private failure")) {
		t.Fatal("panic details exposed")
	}

	preflight := httptest.NewRequest(http.MethodOptions, "/panic", nil)
	preflight.Header.Set(echo.HeaderOrigin, "https://impuls-goroda.github.io")
	preflight.Header.Set(echo.HeaderAccessControlRequestMethod, http.MethodDelete)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, preflight)
	if rec.Code != http.StatusNoContent || rec.Header().Get(echo.HeaderAccessControlAllowOrigin) != "https://impuls-goroda.github.io" {
		t.Fatalf("preflight status=%d origin=%q", rec.Code, rec.Header().Get(echo.HeaderAccessControlAllowOrigin))
	}
	if !strings.Contains(rec.Header().Get(echo.HeaderAccessControlAllowMethods), http.MethodDelete) {
		t.Fatalf("preflight methods=%q", rec.Header().Get(echo.HeaderAccessControlAllowMethods))
	}

	denied := httptest.NewRequest(http.MethodOptions, "/panic", nil)
	denied.Header.Set(echo.HeaderOrigin, "https://evil.example")
	denied.Header.Set(echo.HeaderAccessControlRequestMethod, http.MethodGet)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, denied)
	if rec.Header().Get(echo.HeaderAccessControlAllowOrigin) != "" {
		t.Fatal("unapproved origin allowed")
	}
}

func TestRequestIDRejectsUnsafeInput(t *testing.T) {
	e := testEcho()
	e.GET("/", func(c *echo.Context) error { return c.String(http.StatusOK, RequestID(c)) })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(echo.HeaderXRequestID, "unsafe value")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() == "unsafe value" || rec.Body.Len() == 0 {
		t.Fatalf("request ID response=%q", rec.Body.String())
	}
}
