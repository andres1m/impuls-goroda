package httpapi

import (
	"context"
	"net/http"

	"github.com/andres1m/impuls-goroda/pkg/router"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/app"
	"github.com/labstack/echo/v5"
)

type ReadinessRuntime interface {
	AnonymousLimiter
	Readiness(context.Context) app.OperationReadiness
}

type ReadinessRouter struct{ runtime ReadinessRuntime }

func NewReadinessRouter(runtime ReadinessRuntime) *ReadinessRouter {
	return &ReadinessRouter{runtime: runtime}
}

func (r *ReadinessRouter) Routes() []router.Route {
	return []router.Route{
		router.NewRoute(http.MethodGet, "/health/ready", r.readiness, shareNoStore, AnonymousRateLimit(r.runtime)),
	}
}

func (r *ReadinessRouter) readiness() echo.HandlerFunc {
	return func(c *echo.Context) error {
		value := r.runtime.Readiness(c.Request().Context())
		status := http.StatusOK
		if value.Status == "unavailable" {
			status = http.StatusServiceUnavailable
		}
		return c.JSON(status, struct {
			RequestID string `json:"request_id"`
			app.OperationReadiness
		}{RequestID(c), value})
	}
}
