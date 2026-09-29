package httpapi

import (
	"context"
	"net/http"

	"github.com/andres1m/impuls-goroda/pkg/router"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/app"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/labstack/echo/v5"
)

type PanicRuntime interface {
	Authenticator
	ProposePanic(context.Context, app.RouteCommand, routewire.PanicInput, string) (command.Result, error)
}

type PanicRouter struct{ runtime PanicRuntime }

func NewPanicRouter(runtime PanicRuntime) *PanicRouter { return &PanicRouter{runtime: runtime} }

func (r *PanicRouter) Routes() []router.Route {
	return []router.Route{
		router.NewRoute(http.MethodPost, "/routes/reroute/panic", func() echo.HandlerFunc { return r.panic }, Authenticate(r.runtime), AuthenticatedRateLimit(r.runtime)),
	}
}

func (r *PanicRouter) panic(c *echo.Context) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	principal, ok := PrincipalFrom(c)
	if !ok {
		return authRequired()
	}
	key, err := ParseIdempotencyKey(c.Request())
	if err != nil {
		return err
	}
	revision, err := ParseIfMatch(c.Request())
	if err != nil {
		return err
	}
	raw, err := proposalBody(c)
	if err != nil {
		return err
	}
	input, err := routewire.DecodePanicInput(raw)
	if err != nil {
		return malformedCommandHeader()
	}
	id, err := pathUUID(input.RouteID)
	if err != nil {
		return malformedCommandHeader()
	}
	target := app.RouteCommand{ActorID: principal.UserID, RouteID: d.RouteID(id), ExpectedRevision: revision, Key: key}
	result, err := r.runtime.ProposePanic(c.Request().Context(), target, input, RequestID(c))
	return sendProposalResult(c, result, err, false)
}
