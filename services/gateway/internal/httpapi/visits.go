package httpapi

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/andres1m/impuls-goroda/pkg/router"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/app"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/labstack/echo/v5"
)

const maxVisitBodyBytes = 16 * 1024

type VisitRuntime interface {
	Authenticator
	UpdateParticipation(context.Context, app.VisitCommand, app.ParticipationInput) (command.Result, error)
	UpdateExecution(context.Context, app.VisitCommand, app.ExecutionInput) (command.Result, error)
}

type VisitRouter struct {
	runtime VisitRuntime
}

func NewVisitRouter(runtime VisitRuntime) *VisitRouter { return &VisitRouter{runtime: runtime} }

func (r *VisitRouter) Routes() []router.Route {
	return []router.Route{
		router.NewRoute(http.MethodPost, "/routes/:route_id/visits/:visit_id/participation", r.participation, Authenticate(r.runtime), AuthenticatedRateLimit(r.runtime)),
		router.NewRoute(http.MethodPost, "/routes/:route_id/visits/:visit_id/execution", r.execution, Authenticate(r.runtime), AuthenticatedRateLimit(r.runtime)),
	}
}

func (r *VisitRouter) participation() echo.HandlerFunc {
	return func(c *echo.Context) error {
		target, err := visitTarget(c)
		if err != nil {
			return err
		}
		var input app.ParticipationInput
		if err := decodeVisitBody(c, &input); err != nil {
			return err
		}
		result, err := r.runtime.UpdateParticipation(c.Request().Context(), target, input)
		if err != nil {
			return MapCommandError(err)
		}
		return sendVisitResult(c, result)
	}
}

func (r *VisitRouter) execution() echo.HandlerFunc {
	return func(c *echo.Context) error {
		target, err := visitTarget(c)
		if err != nil {
			return err
		}
		var input app.ExecutionInput
		if err := decodeVisitBody(c, &input); err != nil {
			return err
		}
		result, err := r.runtime.UpdateExecution(c.Request().Context(), target, input)
		if err != nil {
			return MapCommandError(err)
		}
		return sendVisitResult(c, result)
	}
}

func visitTarget(c *echo.Context) (app.VisitCommand, error) {
	principal, ok := PrincipalFrom(c)
	if !ok {
		return app.VisitCommand{}, authRequired()
	}
	routeID, err := pathUUID(c.Param("route_id"))
	if err != nil {
		return app.VisitCommand{}, malformedCommandHeader()
	}
	visitID, err := pathUUID(c.Param("visit_id"))
	if err != nil {
		return app.VisitCommand{}, malformedCommandHeader()
	}
	key, err := ParseIdempotencyKey(c.Request())
	if err != nil {
		return app.VisitCommand{}, err
	}
	revision, err := ParseIfMatch(c.Request())
	if err != nil {
		return app.VisitCommand{}, err
	}
	return app.VisitCommand{
		ActorID: principal.UserID, RouteID: domain.RouteID(routeID), VisitID: domain.VisitID(visitID),
		ExpectedRevision: revision, Key: key,
	}, nil
}

func pathUUID(raw string) ([16]byte, error) {
	if len(raw) != 36 || raw[8] != '-' || raw[13] != '-' || raw[18] != '-' || raw[23] != '-' {
		return [16]byte{}, errors.New("invalid identifier")
	}
	decoded, err := hex.DecodeString(strings.ReplaceAll(raw, "-", ""))
	if err != nil || len(decoded) != 16 {
		return [16]byte{}, errors.New("invalid identifier")
	}
	var id [16]byte
	copy(id[:], decoded)
	if id == ([16]byte{}) {
		return [16]byte{}, errors.New("invalid identifier")
	}
	return id, nil
}

func decodeVisitBody(c *echo.Context, destination any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(c.Response(), c.Request().Body, maxVisitBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return malformedCommandHeader()
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return malformedCommandHeader()
	}
	return nil
}

func sendVisitResult(c *echo.Context, result command.Result) error {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(result.ResponseBody, &body); err != nil {
		return err
	}
	requestID, err := json.Marshal(RequestID(c))
	if err != nil {
		return err
	}
	body["request_id"] = requestID
	if result.ResultingRevision != nil {
		c.Response().Header().Set("ETag", `"`+strconv.FormatInt(int64(*result.ResultingRevision), 10)+`"`)
	}
	return c.JSON(result.HTTPStatus, body)
}
