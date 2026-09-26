package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/auth"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

const principalKey = "principal"

type Principal struct {
	UserID domain.UserID
	Kind   domain.AccountKind
}

type Authenticator interface {
	Authenticate(context.Context, string) (auth.SessionAccount, error)
	AllowAuthenticated(domain.UserID, time.Time) (bool, time.Duration)
}

type AnonymousLimiter interface {
	AllowAnonymous(string, time.Time) (bool, time.Duration)
}

type OwnerChecker interface {
	RequireRouteOwner(context.Context, domain.RouteID, domain.UserID) error
}

type WebhookChecker interface {
	WebhookValid(string) bool
}

func Authenticate(runtime Authenticator) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			values := c.Request().Header.Values(echo.HeaderAuthorization)
			if len(values) != 1 {
				return authRequired()
			}
			parts := strings.Fields(values[0])
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) != 43 {
				return authRequired()
			}
			pair, err := runtime.Authenticate(c.Request().Context(), parts[1])
			if err != nil {
				if errors.Is(err, auth.ErrAuthRequired) {
					return authRequired()
				}
				return &Error{Status: http.StatusServiceUnavailable, Code: "AUTH_UNAVAILABLE", Message: "Authentication is temporarily unavailable", Retryable: true, Cause: err}
			}
			c.Set(principalKey, Principal{UserID: pair.Account.ID, Kind: pair.Account.Kind})
			return next(c)
		}
	}
}

func AuthenticatedRateLimit(runtime Authenticator) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			principal, ok := PrincipalFrom(c)
			if !ok {
				return authRequired()
			}
			allowed, retryAfter := runtime.AllowAuthenticated(principal.UserID, time.Now())
			if !allowed {
				return rateLimited(retryAfter)
			}
			return next(c)
		}
	}
}

func AnonymousRateLimit(runtime AnonymousLimiter) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			allowed, retryAfter := runtime.AllowAnonymous(strings.TrimSpace(c.RealIP()), time.Now())
			if !allowed {
				return rateLimited(retryAfter)
			}
			return next(c)
		}
	}
}

func VerifyWebhook(runtime WebhookChecker) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if !runtime.WebhookValid(c.Request().Header.Get("X-Max-Bot-Api-Secret")) {
				return authRequired()
			}
			return next(c)
		}
	}
}

func RequireOwner(ctx context.Context, runtime OwnerChecker, routeID domain.RouteID, userID domain.UserID) error {
	if err := runtime.RequireRouteOwner(ctx, routeID, userID); err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			return &Error{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "Resource not found"}
		}
		return &Error{Status: http.StatusServiceUnavailable, Code: "DATABASE_UNAVAILABLE", Message: "Service is temporarily unavailable", Retryable: true, Cause: err}
	}
	return nil
}

func PrincipalFrom(c *echo.Context) (Principal, bool) {
	principal, ok := c.Get(principalKey).(Principal)
	return principal, ok
}

func CORS(allowedOrigin string) (echo.MiddlewareFunc, error) {
	return (middleware.CORSConfig{
		AllowOrigins: []string{allowedOrigin},
		AllowMethods: []string{http.MethodGet, http.MethodPost, http.MethodDelete, http.MethodOptions},
		AllowHeaders: []string{
			echo.HeaderAuthorization,
			echo.HeaderContentType,
			echo.HeaderXRequestID,
			"Idempotency-Key",
			"If-Match",
		},
		ExposeHeaders: []string{echo.HeaderXRequestID, "ETag", "Retry-After"},
		MaxAge:        600,
	}).ToMiddleware()
}

func authRequired() *Error {
	return &Error{Status: http.StatusUnauthorized, Code: "AUTH_REQUIRED", Message: "Authentication is required"}
}

func rateLimited(delay time.Duration) *Error {
	seconds := int(delay.Round(time.Second) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return &Error{Status: http.StatusTooManyRequests, Code: "RATE_LIMITED", Message: "Too many requests", Retryable: true, RetryAfter: seconds}
}
