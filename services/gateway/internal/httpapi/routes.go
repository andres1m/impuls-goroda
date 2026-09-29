package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/andres1m/impuls-goroda/pkg/router"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/app"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/optimizerclient"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/labstack/echo/v5"
)

type RouteRuntime interface {
	Authenticator
	SaveRoute(context.Context, app.RouteCommand) (command.Result, error)
	OptimizeRoutes(context.Context, domain.UserID, [16]byte, routewire.ConfirmedRouteInput, string) (command.Result, error)
	ReadRoute(context.Context, domain.UserID, domain.RouteID) (routewire.OwnerRoute, error)
}

type RouteRouter struct{ runtime RouteRuntime }

func NewRouteRouter(runtime RouteRuntime) *RouteRouter { return &RouteRouter{runtime: runtime} }

func (r *RouteRouter) Routes() []router.Route {
	return []router.Route{
		router.NewRoute(http.MethodPost, "/routes/optimize", func() echo.HandlerFunc { return r.optimize }, Authenticate(r.runtime), AuthenticatedRateLimit(r.runtime)),
		router.NewRoute(http.MethodGet, "/routes/:route_id", func() echo.HandlerFunc { return r.read }, Authenticate(r.runtime), AuthenticatedRateLimit(r.runtime)),
		router.NewRoute(http.MethodPost, "/routes/:route_id/save", func() echo.HandlerFunc { return r.save }, Authenticate(r.runtime), AuthenticatedRateLimit(r.runtime)),
	}
}

func (r *RouteRouter) optimize(c *echo.Context) error {
	principal, ok := PrincipalFrom(c)
	if !ok {
		return authRequired()
	}
	key, err := ParseIdempotencyKey(c.Request())
	if err != nil {
		return err
	}
	if c.Request().Body == nil {
		return malformedCommandHeader()
	}
	raw, err := io.ReadAll(http.MaxBytesReader(c.Response(), c.Request().Body, 4<<20))
	if err != nil {
		return malformedCommandHeader()
	}
	input, err := routewire.DecodeInput(raw)
	if err != nil {
		return malformedCommandHeader()
	}
	result, err := r.runtime.OptimizeRoutes(c.Request().Context(), principal.UserID, key, input, RequestID(c))
	if err != nil {
		return mapRouteError(err)
	}
	return sendVisitResult(c, result)
}

func (r *RouteRouter) read(c *echo.Context) error {
	principal, ok := PrincipalFrom(c)
	if !ok {
		return authRequired()
	}
	id, err := pathUUID(c.Param("route_id"))
	if err != nil {
		return malformedCommandHeader()
	}
	route, err := r.runtime.ReadRoute(c.Request().Context(), principal.UserID, domain.RouteID(id))
	if err != nil {
		return mapRouteError(err)
	}
	c.Response().Header().Set("ETag", `"`+route.Revision+`"`)
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, struct {
		RequestID string               `json:"request_id"`
		Route     routewire.OwnerRoute `json:"route"`
	}{RequestID(c), route})
}

func mapRouteError(err error) error {
	switch {
	case errors.Is(err, optimizerclient.ErrInvalidInput), errors.Is(err, postgres.ErrCityTimezone):
		return &Error{Status: http.StatusBadRequest, Code: "INVALID_INPUT", Message: "Route input is invalid"}
	case errors.Is(err, optimizerclient.ErrMessageTooLarge):
		return &Error{Status: http.StatusBadRequest, Code: "REQUEST_TOO_LARGE", Message: "Calculation request is too large"}
	case errors.Is(err, optimizerclient.ErrDeadline):
		return &Error{Status: http.StatusGatewayTimeout, Code: "OPTIMIZER_TIMEOUT", Message: "Calculation timed out", Retryable: true}
	case errors.Is(err, optimizerclient.ErrCanceled), errors.Is(err, context.Canceled):
		return &Error{Status: http.StatusRequestTimeout, Code: "REQUEST_CANCELED", Message: "Calculation was canceled", Retryable: true}
	case errors.Is(err, optimizerclient.ErrBusy):
		return &Error{Status: http.StatusServiceUnavailable, Code: "OPTIMIZER_BUSY", Message: "Calculation is busy", Retryable: true, RetryAfter: 1}
	case errors.Is(err, optimizerclient.ErrUnavailable):
		return &Error{Status: http.StatusServiceUnavailable, Code: "OPTIMIZER_UNAVAILABLE", Message: "Calculation is unavailable", Retryable: true}
	case errors.Is(err, postgres.ErrCatalogChanged):
		return &Error{Status: http.StatusServiceUnavailable, Code: "CATALOG_CHANGED", Message: "Catalog changed; retry the calculation", Retryable: true}
	case errors.Is(err, optimizerclient.ErrInvalidResponse), errors.Is(err, routewire.ErrInvalidResult):
		return &Error{Status: http.StatusBadGateway, Code: "INVALID_CALCULATION_RESULT", Message: "Calculation returned an invalid result", Retryable: true}
	default:
		return MapCommandError(err)
	}
}

func (r *RouteRouter) save(c *echo.Context) error {
	if c.Request().Body != nil {
		body, err := io.ReadAll(io.LimitReader(c.Request().Body, 1))
		if err != nil || len(body) != 0 {
			return &Error{Status: http.StatusBadRequest, Code: "MALFORMED_REQUEST", Message: "Save command does not accept a body"}
		}
	}
	principal, ok := PrincipalFrom(c)
	if !ok {
		return authRequired()
	}
	routeID, err := pathUUID(c.Param("route_id"))
	if err != nil {
		return malformedCommandHeader()
	}
	key, err := ParseIdempotencyKey(c.Request())
	if err != nil {
		return err
	}
	revision, err := ParseIfMatch(c.Request())
	if err != nil {
		return err
	}
	result, err := r.runtime.SaveRoute(c.Request().Context(), app.RouteCommand{
		ActorID: principal.UserID, RouteID: domain.RouteID(routeID), ExpectedRevision: revision, Key: key,
	})
	if err != nil {
		return MapCommandError(err)
	}
	return sendVisitResult(c, result)
}
