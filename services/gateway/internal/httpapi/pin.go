package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/andres1m/impuls-goroda/pkg/router"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/app"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/labstack/echo/v5"
)

type PinRuntime interface {
	Authenticator
	SetVisitPin(context.Context, app.VisitCommand, routewire.PinVisitInput, string) (command.Result, error)
}

type PinRouter struct{ runtime PinRuntime }

func NewPinRouter(runtime PinRuntime) *PinRouter { return &PinRouter{runtime: runtime} }

func (r *PinRouter) Routes() []router.Route {
	return []router.Route{
		router.NewRoute(http.MethodPost, "/routes/:route_id/visits/:visit_id/pin", func() echo.HandlerFunc { return r.pin }, Authenticate(r.runtime), AuthenticatedRateLimit(r.runtime)),
	}
}

func (r *PinRouter) pin(c *echo.Context) error {
	target, err := visitTarget(c)
	if err != nil {
		return err
	}
	if c.Request().Body == nil {
		return malformedCommandHeader()
	}
	raw, err := io.ReadAll(http.MaxBytesReader(c.Response(), c.Request().Body, maxVisitBodyBytes))
	if err != nil {
		return malformedCommandHeader()
	}
	input, err := routewire.DecodePinVisitInput(raw)
	if err != nil {
		return malformedCommandHeader()
	}
	result, err := r.runtime.SetVisitPin(c.Request().Context(), target, input, RequestID(c))
	if errors.Is(err, routewire.ErrInvalidRecomputeInput) {
		return &Error{Status: http.StatusUnprocessableEntity, Code: "INVALID_INPUT", Message: "Route state cannot be recomputed"}
	}
	if err != nil {
		return mapRouteError(err)
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return sendVisitResult(c, result)
}
