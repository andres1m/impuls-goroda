package httpapi

import (
	"errors"
	"io"
	"net/http"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/labstack/echo/v5"
)

func (r *ScenarioRouter) saveDraft(c *echo.Context) error {
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
	input, err := routewire.DecodeSaveBotScenarioDraftInput(raw)
	if err != nil {
		return &Error{Status: http.StatusBadRequest, Code: "INVALID_INPUT", Message: "Scenario input is invalid"}
	}
	result, err := r.runtime.SaveBotScenarioDraft(c.Request().Context(), principal.UserID, c.Param("scenario_id"), key, input)
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
	return sendVisitResult(c, result)
}
