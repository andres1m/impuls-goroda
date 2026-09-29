package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/andres1m/impuls-goroda/pkg/router"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/labstack/echo/v5"
)

type ScenarioRuntime interface {
	Authenticator
	ReadBotScenario(context.Context, d.UserID, string) (routewire.BotScenario, error)
	ReadLatestBotScenario(context.Context, d.UserID) (*routewire.BotScenario, error)
	CompleteBotScenario(context.Context, d.UserID, string, [16]byte, routewire.CompleteBotScenarioInput, string) (command.Result, error)
	SaveBotScenarioDraft(context.Context, d.UserID, string, [16]byte, routewire.SaveBotScenarioDraftInput) (command.Result, error)
}

type ScenarioRouter struct{ runtime ScenarioRuntime }

func NewScenarioRouter(runtime ScenarioRuntime) *ScenarioRouter {
	return &ScenarioRouter{runtime: runtime}
}

func (r *ScenarioRouter) Routes() []router.Route {
	return []router.Route{
		router.NewRoute(http.MethodPost, "/scenarios/:scenario_id/draft", func() echo.HandlerFunc { return r.saveDraft }, shareNoStore, Authenticate(r.runtime), AuthenticatedRateLimit(r.runtime)),
		router.NewRoute(http.MethodGet, "/me/scenario", func() echo.HandlerFunc { return r.read(true) }, Authenticate(r.runtime), AuthenticatedRateLimit(r.runtime)),
		router.NewRoute(http.MethodGet, "/scenarios/:scenario_id", func() echo.HandlerFunc { return r.read(false) }, Authenticate(r.runtime), AuthenticatedRateLimit(r.runtime)),
		router.NewRoute(http.MethodPost, "/scenarios/:scenario_id/complete", func() echo.HandlerFunc { return r.complete }, Authenticate(r.runtime), AuthenticatedRateLimit(r.runtime)),
	}
}

func (r *ScenarioRouter) complete(c *echo.Context) error {
	c.Response().Header().Set("Cache-Control", "no-store")
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
	input, err := routewire.DecodeCompleteBotScenarioInput(raw)
	if err != nil {
		return &Error{Status: http.StatusBadRequest, Code: "INVALID_INPUT", Message: "Scenario input is invalid"}
	}
	result, err := r.runtime.CompleteBotScenario(c.Request().Context(), principal.UserID, c.Param("scenario_id"), key, input, RequestID(c))
	if err != nil {
		var conflict *command.ScenarioVersionConflict
		if errors.As(err, &conflict) {
			return &Error{Status: http.StatusConflict, Code: "SCENARIO_VERSION_CONFLICT", Message: "Scenario version is stale", CurrentVersion: conflict.Current}
		}
		if errors.Is(err, command.ErrScenarioCompleted) {
			return &Error{Status: http.StatusConflict, Code: "SCENARIO_ALREADY_COMPLETED", Message: "Scenario is already completed"}
		}
		return mapRouteError(err)
	}
	return sendVisitResult(c, &result)
}

func (r *ScenarioRouter) read(latest bool) echo.HandlerFunc {
	return func(c *echo.Context) error {
		c.Response().Header().Set("Cache-Control", "no-store")
		principal, ok := PrincipalFrom(c)
		if !ok {
			return authRequired()
		}
		var scenario *routewire.BotScenario
		var err error
		if latest {
			scenario, err = r.runtime.ReadLatestBotScenario(c.Request().Context(), principal.UserID)
		} else {
			var value routewire.BotScenario
			value, err = r.runtime.ReadBotScenario(c.Request().Context(), principal.UserID, c.Param("scenario_id"))
			scenario = &value
		}
		if err != nil {
			return MapCommandError(err)
		}
		return c.JSON(http.StatusOK, struct {
			RequestID string                 `json:"request_id"`
			Scenario  *routewire.BotScenario `json:"scenario"`
		}{RequestID(c), scenario})
	}
}
