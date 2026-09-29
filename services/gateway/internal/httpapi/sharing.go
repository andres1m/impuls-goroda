package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/andres1m/impuls-goroda/pkg/router"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/app"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/optimizerclient"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/sharewire"
	"github.com/labstack/echo/v5"
)

type ShareRuntime interface {
	Authenticator
	AnonymousLimiter
	ReadSharedRoute(context.Context, sharewire.Token) (sharewire.SharedRoute, error)
	CreateShare(context.Context, app.ShareCommand, sharewire.CreateInput) (command.Result, error)
	RevokeShare(context.Context, app.ShareCommand) (command.Result, error)
	CopySharedRoute(context.Context, domain.UserID, [16]byte, sharewire.Token, domain.RouteRevisionNumber,
		sharewire.CopyInput, string) (command.Result, error)
}

type ShareRouter struct {
	runtime  ShareRuntime
	username string
}

func NewShareRouter(runtime ShareRuntime, username string) *ShareRouter {
	return &ShareRouter{runtime: runtime, username: username}
}

func (r *ShareRouter) Routes() []router.Route {
	return []router.Route{
		router.NewRoute(http.MethodGet, "/shared-routes/:share_token", r.read,
			shareNoStore, AnonymousRateLimit(r.runtime)),
		router.NewRoute(http.MethodPost, "/shared-routes/:share_token/copy", r.copy,
			shareNoStore, Authenticate(r.runtime), AuthenticatedRateLimit(r.runtime)),
		router.NewRoute(http.MethodPost, "/routes/:route_id/share", r.create,
			shareNoStore, Authenticate(r.runtime), AuthenticatedRateLimit(r.runtime)),
		router.NewRoute(http.MethodDelete, "/routes/:route_id/share", r.revoke,
			shareNoStore, Authenticate(r.runtime), AuthenticatedRateLimit(r.runtime)),
	}
}

func (r *ShareRouter) copy() echo.HandlerFunc {
	return func(c *echo.Context) error {
		principal, ok := PrincipalFrom(c)
		if !ok {
			return authRequired()
		}
		token, err := sharewire.ParseToken(c.Param("share_token"))
		if err != nil {
			return shareNotFound()
		}
		key, err := ParseIdempotencyKey(c.Request())
		if err != nil {
			return err
		}
		revision, err := ParseIfMatch(c.Request())
		if err != nil {
			return err
		}
		raw, err := io.ReadAll(http.MaxBytesReader(c.Response(), c.Request().Body, 16*1024))
		if err != nil {
			return malformedCommandHeader()
		}
		input, err := sharewire.DecodeCopyInput(raw)
		if err != nil {
			return invalidShareRequest()
		}
		result, err := r.runtime.CopySharedRoute(c.Request().Context(), principal.UserID, key,
			token, revision, input, RequestID(c))
		if refused, ok := errors.AsType[*app.CopyRefusal](err); ok {
			return c.JSON(http.StatusUnprocessableEntity, struct {
				Status    string               `json:"status"`
				RequestID string               `json:"request_id"`
				Conflicts []routewire.Conflict `json:"conflicts"`
			}{refused.Status, RequestID(c), refused.Conflicts})
		}
		if err != nil {
			return mapCopyError(err)
		}
		if result.HTTPStatus != http.StatusOK || result.RouteID == nil || result.ResultingRevision == nil {
			return errors.New("invalid copy result")
		}
		var saved struct {
			Status string               `json:"status"`
			Route  routewire.OwnerRoute `json:"route"`
		}
		if err := json.Unmarshal(result.ResponseBody, &saved); err != nil ||
			saved.Route.RouteID == "" || saved.Route.Revision == "" ||
			(saved.Status != "READY" && saved.Status != "PARTIAL") {
			return errors.New("invalid copy response template")
		}
		c.Response().Header().Set("ETag", `"`+saved.Route.Revision+`"`)
		return c.JSON(http.StatusOK, struct {
			Status    string               `json:"status"`
			RequestID string               `json:"request_id"`
			Route     routewire.OwnerRoute `json:"route"`
		}{saved.Status, RequestID(c), saved.Route})
	}
}

func mapCopyError(err error) error {
	if errors.Is(err, postgres.ErrNotFound) {
		return shareNotFound()
	}
	if errors.Is(err, optimizerclient.ErrInvalidInput) {
		return invalidShareRequest()
	}
	return mapRouteError(err)
}

func (r *ShareRouter) read() echo.HandlerFunc {
	return func(c *echo.Context) error {
		token, err := sharewire.ParseToken(c.Param("share_token"))
		if err != nil {
			return shareNotFound()
		}
		route, err := r.runtime.ReadSharedRoute(c.Request().Context(), token)
		if errors.Is(err, postgres.ErrNotFound) {
			return shareNotFound()
		}
		if err != nil {
			return &Error{Status: http.StatusServiceUnavailable, Code: "SHARING_UNAVAILABLE",
				Message: "Sharing is temporarily unavailable", Retryable: true}
		}
		c.Response().Header().Set("ETag", `"`+route.Revision+`"`)
		return c.JSON(http.StatusOK, struct {
			RequestID string                `json:"request_id"`
			Route     sharewire.SharedRoute `json:"route"`
		}{RequestID(c), route})
	}
}

func shareNotFound() *Error {
	return &Error{Status: http.StatusNotFound, Code: "SHARE_NOT_FOUND", Message: "Shared route is unavailable"}
}

func shareNoStore(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		c.Response().Header().Set("Cache-Control", "no-store")
		return next(c)
	}
}

func (r *ShareRouter) create() echo.HandlerFunc {
	return func(c *echo.Context) error {
		target, err := shareTarget(c)
		if err != nil {
			return err
		}
		if r.username == "" || strings.ContainsAny(r.username, "/?# \t\r\n") || r.username == "." || r.username == ".." {
			return &Error{Status: http.StatusServiceUnavailable, Code: "SHARING_UNAVAILABLE",
				Message: "Sharing is temporarily unavailable", Retryable: true}
		}
		raw, err := io.ReadAll(http.MaxBytesReader(c.Response(), c.Request().Body, 16*1024))
		if err != nil || !utf8.Valid(raw) || !json.Valid(raw) {
			return malformedCommandHeader()
		}
		input, err := sharewire.DecodeCreateInput(raw)
		if err != nil {
			return invalidShareRequest()
		}
		result, err := r.runtime.CreateShare(c.Request().Context(), target, input)
		if err != nil {
			return mapShareError(err)
		}
		return r.sendCreate(c, target, input.Token, result)
	}
}

func (r *ShareRouter) revoke() echo.HandlerFunc {
	return func(c *echo.Context) error {
		target, err := shareTarget(c)
		if err != nil {
			return err
		}
		result, err := r.runtime.RevokeShare(c.Request().Context(), target)
		if err != nil {
			return mapShareError(err)
		}
		if result.HTTPStatus != http.StatusNoContent || !shareResultMatches(target, result) {
			return errors.New("invalid revoke share result")
		}
		return c.NoContent(http.StatusNoContent)
	}
}

type createShareResponse struct {
	Status     string     `json:"status"`
	Revision   string     `json:"revision"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RequestID  string     `json:"request_id"`
	ShareToken string     `json:"share_token"`
	DeepLink   string     `json:"deep_link"`
}

func (r *ShareRouter) sendCreate(c *echo.Context, target app.ShareCommand, token sharewire.Token, result command.Result) error {
	if result.HTTPStatus != http.StatusOK || !shareResultMatches(target, result) {
		return errors.New("invalid create share result")
	}
	var metadata struct {
		Status    string     `json:"status"`
		Revision  string     `json:"revision"`
		CreatedAt time.Time  `json:"created_at"`
		ExpiresAt *time.Time `json:"expires_at,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(result.ResponseBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil {
		return errors.New("invalid share response template")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("invalid share response template")
	}
	revision := strconv.FormatInt(int64(*result.ResultingRevision), 10)
	if (metadata.Status != "READY" && metadata.Status != "UNCHANGED") || metadata.Revision != revision ||
		metadata.CreatedAt.IsZero() || (metadata.ExpiresAt != nil && !metadata.ExpiresAt.After(metadata.CreatedAt)) {
		return errors.New("invalid share response metadata")
	}
	if _, err := token.Hash(); err != nil {
		return errors.New("invalid share response token")
	}
	query := url.Values{"startapp": {token.Raw()}}
	response := createShareResponse{Status: metadata.Status, Revision: revision,
		CreatedAt: metadata.CreatedAt, ExpiresAt: metadata.ExpiresAt, RequestID: RequestID(c),
		ShareToken: token.Raw(), DeepLink: "https://max.ru/" + url.PathEscape(r.username) + "?" + query.Encode()}
	c.Response().Header().Set("ETag", `"`+revision+`"`)
	return c.JSON(http.StatusOK, response)
}

func shareResultMatches(target app.ShareCommand, result command.Result) bool {
	return result.RouteID != nil && *result.RouteID == target.RouteID &&
		result.ResultingRevision != nil && *result.ResultingRevision == target.ExpectedRevision
}

func shareTarget(c *echo.Context) (app.ShareCommand, error) {
	principal, ok := PrincipalFrom(c)
	if !ok {
		return app.ShareCommand{}, authRequired()
	}
	id, err := pathUUID(c.Param("route_id"))
	if err != nil {
		return app.ShareCommand{}, malformedCommandHeader()
	}
	key, err := ParseIdempotencyKey(c.Request())
	if err != nil {
		return app.ShareCommand{}, err
	}
	revision, err := ParseIfMatch(c.Request())
	if err != nil {
		return app.ShareCommand{}, err
	}
	return app.ShareCommand{ActorID: principal.UserID, RouteID: domain.RouteID(id),
		ExpectedRevision: revision, Key: key}, nil
}

func mapShareError(err error) error {
	if errors.Is(err, postgres.ErrInvalidShare) {
		return invalidShareRequest()
	}
	if errors.Is(err, postgres.ErrNotFound) {
		return &Error{Status: http.StatusNotFound, Code: "ROUTE_NOT_FOUND", Message: "Route not found"}
	}
	return MapCommandError(err)
}

func invalidShareRequest() *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: "VALIDATION_FAILED", Message: "Share request is invalid"}
}
