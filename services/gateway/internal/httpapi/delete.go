package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/andres1m/impuls-goroda/pkg/router"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/app"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/labstack/echo/v5"
)

type DeleteRuntime interface {
	Authenticator
	DeleteRoute(context.Context, app.RouteCommand, routewire.DeleteRouteInput) (command.Result, error)
}

type DeleteRouter struct{ runtime DeleteRuntime }

func NewDeleteRouter(runtime DeleteRuntime) *DeleteRouter { return &DeleteRouter{runtime: runtime} }

func (r *DeleteRouter) Routes() []router.Route {
	return []router.Route{router.NewRoute(http.MethodPost, "/routes/:route_id/delete", func() echo.HandlerFunc { return r.delete },
		shareNoStore, Authenticate(r.runtime), AuthenticatedRateLimit(r.runtime))}
}

func (r *DeleteRouter) delete(c *echo.Context) error {
	target, err := proposalTarget(c)
	if err != nil {
		return err
	}
	raw, err := proposalBody(c)
	if err != nil {
		return err
	}
	input, err := routewire.DecodeDeleteRouteInput(raw)
	if err != nil {
		return malformedCommandHeader()
	}
	_, err = r.runtime.DeleteRoute(c.Request().Context(), target, input)
	if errors.Is(err, routewire.ErrExternalCommitmentAcknowledgementRequired) {
		return &Error{Status: http.StatusConflict, Code: "EXTERNAL_COMMITMENT_CONFIRMATION_REQUIRED", Message: "Confirm that deleting the route does not cancel external bookings"}
	}
	if err != nil {
		return mapRouteError(err)
	}
	return c.NoContent(http.StatusNoContent)
}
