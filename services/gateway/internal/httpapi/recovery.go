package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/andres1m/impuls-goroda/pkg/router"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/app"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/labstack/echo/v5"
)

type RecoveryRuntime interface {
	Authenticator
	ListRoutes(context.Context, domain.UserID, app.RoutePageRequest) (routewire.RoutePage, error)
	ReadScenarioContext(context.Context, domain.UserID) (routewire.ScenarioContext, error)
	SelectScenarioRoute(context.Context, app.ScenarioSelection) (command.Result, error)
}

type RecoveryRouter struct{ runtime RecoveryRuntime }

func NewRecoveryRouter(runtime RecoveryRuntime) *RecoveryRouter {
	return &RecoveryRouter{runtime: runtime}
}

func (r *RecoveryRouter) Routes() []router.Route {
	return []router.Route{
		router.NewRoute(http.MethodGet, "/routes", r.list, Authenticate(r.runtime), AuthenticatedRateLimit(r.runtime)),
		router.NewRoute(http.MethodGet, "/me/context", r.context, Authenticate(r.runtime), AuthenticatedRateLimit(r.runtime)),
		router.NewRoute(http.MethodPost, "/me/context/selection", r.selectRoute, Authenticate(r.runtime), AuthenticatedRateLimit(r.runtime)),
	}
}

func (r *RecoveryRouter) context() echo.HandlerFunc {
	return func(c *echo.Context) error {
		principal, ok := PrincipalFrom(c)
		if !ok {
			return authRequired()
		}
		value, err := r.runtime.ReadScenarioContext(c.Request().Context(), principal.UserID)
		if err != nil {
			return MapCommandError(err)
		}
		c.Response().Header().Set("Cache-Control", "no-store")
		return c.JSON(http.StatusOK, struct {
			RequestID string `json:"request_id"`
			routewire.ScenarioContext
		}{RequestID(c), value})
	}
}

func (r *RecoveryRouter) selectRoute() echo.HandlerFunc {
	return func(c *echo.Context) error {
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
		if c.Request().Body == nil {
			return malformedCommandHeader()
		}
		raw, err := io.ReadAll(http.MaxBytesReader(c.Response(), c.Request().Body, 1024))
		if err != nil {
			return malformedCommandHeader()
		}
		id, err := routewire.DecodeScenarioSelection(raw)
		if err != nil {
			return malformedCommandHeader()
		}
		result, err := r.runtime.SelectScenarioRoute(c.Request().Context(), app.ScenarioSelection{
			ActorID: principal.UserID, RouteID: domain.RouteID(id), ExpectedRevision: revision, Key: key,
		})
		if err != nil {
			return MapCommandError(err)
		}
		c.Response().Header().Set("Cache-Control", "no-store")
		return sendVisitResult(c, &result)
	}
}

func (r *RecoveryRouter) list() echo.HandlerFunc {
	return func(c *echo.Context) error {
		principal, ok := PrincipalFrom(c)
		if !ok {
			return authRequired()
		}
		values, err := url.ParseQuery(c.Request().URL.RawQuery)
		if err != nil {
			return malformedCommandHeader()
		}
		for name, items := range values {
			if (name != "lifecycle" && name != "limit" && name != "cursor") || len(items) != 1 || items[0] == "" {
				return malformedCommandHeader()
			}
		}
		request := app.RoutePageRequest{Lifecycle: values.Get("lifecycle"), Cursor: values.Get("cursor"), Limit: 20}
		if raw := values.Get("limit"); raw != "" {
			limit, err := strconv.ParseInt(raw, 10, 32)
			if err != nil || limit < 1 || limit > 100 || strconv.FormatInt(limit, 10) != raw {
				return malformedCommandHeader()
			}
			request.Limit = int(limit)
		}
		page, err := r.runtime.ListRoutes(c.Request().Context(), principal.UserID, request)
		if errors.Is(err, app.ErrInvalidRoutePage) {
			return malformedCommandHeader()
		}
		if err != nil {
			return MapCommandError(err)
		}
		c.Response().Header().Set("Cache-Control", "no-store")
		return c.JSON(http.StatusOK, struct {
			RequestID string `json:"request_id"`
			routewire.RoutePage
		}{RequestID(c), page})
	}
}
