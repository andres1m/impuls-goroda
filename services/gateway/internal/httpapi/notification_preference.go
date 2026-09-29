package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/andres1m/impuls-goroda/pkg/router"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/app"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/labstack/echo/v5"
)

type NotificationPreferenceRuntime interface {
	Authenticator
	ReadNotificationPreference(context.Context, d.UserID, d.RouteID) (routewire.NotificationPreferenceResponse, error)
	SetNotificationPreference(context.Context, app.NotificationPreferenceCommand) (command.Result, error)
}

type NotificationPreferenceRouter struct{ runtime NotificationPreferenceRuntime }

func NewNotificationPreferenceRouter(runtime NotificationPreferenceRuntime) *NotificationPreferenceRouter {
	return &NotificationPreferenceRouter{runtime: runtime}
}

func (r *NotificationPreferenceRouter) Routes() []router.Route {
	return []router.Route{
		router.NewRoute(http.MethodGet, "/routes/:route_id/notifications", r.read, Authenticate(r.runtime), AuthenticatedRateLimit(r.runtime)),
		router.NewRoute(http.MethodPost, "/routes/:route_id/notifications", r.set, Authenticate(r.runtime), AuthenticatedRateLimit(r.runtime)),
	}
}

func (r *NotificationPreferenceRouter) read() echo.HandlerFunc {
	return func(c *echo.Context) error {
		c.Response().Header().Set("Cache-Control", "no-store")
		principal, ok := PrincipalFrom(c)
		if !ok {
			return authRequired()
		}
		id, err := pathUUID(c.Param("route_id"))
		if err != nil {
			return malformedCommandHeader()
		}
		value, err := r.runtime.ReadNotificationPreference(c.Request().Context(), principal.UserID, d.RouteID(id))
		if err != nil {
			return MapCommandError(err)
		}
		c.Response().Header().Set("ETag", `"`+value.Revision+`"`)
		return c.JSON(http.StatusOK, struct {
			RequestID string `json:"request_id"`
			routewire.NotificationPreferenceResponse
		}{RequestID(c), value})
	}
}

func (r *NotificationPreferenceRouter) set() echo.HandlerFunc {
	return func(c *echo.Context) error {
		c.Response().Header().Set("Cache-Control", "no-store")
		principal, ok := PrincipalFrom(c)
		if !ok {
			return authRequired()
		}
		id, err := pathUUID(c.Param("route_id"))
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
		if c.Request().Body == nil {
			return malformedCommandHeader()
		}
		raw, err := io.ReadAll(http.MaxBytesReader(c.Response(), c.Request().Body, 1024))
		if err != nil {
			return malformedCommandHeader()
		}
		input, err := routewire.DecodeNotificationPreference(raw)
		if err != nil {
			return malformedCommandHeader()
		}
		result, err := r.runtime.SetNotificationPreference(c.Request().Context(), app.NotificationPreferenceCommand{
			ActorID: principal.UserID, RouteID: d.RouteID(id), ExpectedRevision: revision, Key: key, Input: input,
		})
		if errors.Is(err, postgres.ErrNotificationPreferenceConflict) {
			return &Error{Status: http.StatusConflict, Code: "NOTIFICATION_PREFERENCE_CONFLICT", Message: "Notification preference changed; reload before saving"}
		}
		if errors.Is(err, postgres.ErrNotificationRouteNotSaved) {
			return &Error{Status: http.StatusConflict, Code: "ROUTE_NOT_SAVED", Message: "Save the route before changing notification settings"}
		}
		if err != nil {
			return MapCommandError(err)
		}
		return sendVisitResult(c, &result)
	}
}
