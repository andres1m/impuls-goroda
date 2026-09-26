package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/andres1m/impuls-goroda/pkg/router"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/auth"
	"github.com/labstack/echo/v5"
)

const maxAuthBodyBytes = 17 * 1024

type AuthRuntime interface {
	ExchangeMax(ctx context.Context, raw string) (auth.IssuedSession, error)
	AllowAnonymous(key string, now time.Time) (bool, time.Duration)
}

type AuthRouter struct {
	runtime AuthRuntime
}

func NewAuthRouter(runtime AuthRuntime) *AuthRouter {
	return &AuthRouter{runtime: runtime}
}

func (r *AuthRouter) Routes() []router.Route {
	return []router.Route{
		router.NewRoute(http.MethodPost, "/auth/max", r.exchangeMax, AnonymousRateLimit(r.runtime)),
	}
}

type maxAuthRequest struct {
	InitData string `json:"init_data"`
}

type authResponse struct {
	AccessToken string    `json:"access_token"`
	TokenType   string    `json:"token_type"`
	ExpiresAt   time.Time `json:"expires_at"`
	RequestID   string    `json:"request_id"`
}

func (r *AuthRouter) exchangeMax() echo.HandlerFunc {
	return func(c *echo.Context) error {
		var request maxAuthRequest
		body := http.MaxBytesReader(c.Response(), c.Request().Body, maxAuthBodyBytes)
		decoder := json.NewDecoder(body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || request.InitData == "" {
			return &Error{Status: http.StatusBadRequest, Code: "MALFORMED_REQUEST", Message: "Request cannot be parsed"}
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return &Error{Status: http.StatusBadRequest, Code: "MALFORMED_REQUEST", Message: "Request cannot be parsed"}
		}
		issued, err := r.runtime.ExchangeMax(c.Request().Context(), request.InitData)
		if err != nil {
			if errors.Is(err, auth.ErrAuthRequired) {
				return &Error{Status: http.StatusUnauthorized, Code: "AUTH_REQUIRED", Message: "Authentication is required"}
			}
			return &Error{Status: http.StatusServiceUnavailable, Code: "AUTH_UNAVAILABLE", Message: "Authentication is temporarily unavailable", Retryable: true, Cause: err}
		}
		return c.JSON(http.StatusOK, authResponse{
			AccessToken: issued.AccessToken,
			TokenType:   "Bearer",
			ExpiresAt:   issued.ExpiresAt,
			RequestID:   RequestID(c),
		})
	}
}
