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
	"github.com/andres1m/impuls-goroda/services/gateway/internal/lunchprovider"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/labstack/echo/v5"
)

type ProposalRuntime interface {
	Authenticator
	ProposeVisitRemoval(context.Context, app.VisitCommand, routewire.RemovalProposalInput, string) (command.Result, error)
	ProposeLunchChange(context.Context, app.RouteCommand, routewire.LunchProposalInput, string) (command.Result, error)
	ApplyRouteProposal(context.Context, app.RouteCommand, d.ProposalID) (command.Result, error)
	RejectRouteProposal(context.Context, app.RouteCommand, d.ProposalID) (command.Result, error)
}

type ProposalRouter struct{ runtime ProposalRuntime }

func NewProposalRouter(runtime ProposalRuntime) *ProposalRouter {
	return &ProposalRouter{runtime: runtime}
}

func (r *ProposalRouter) Routes() []router.Route {
	return []router.Route{
		router.NewRoute(http.MethodPost, "/routes/:route_id/visits/:visit_id/removal-proposal", func() echo.HandlerFunc { return r.removal }, Authenticate(r.runtime), AuthenticatedRateLimit(r.runtime)),
		router.NewRoute(http.MethodPost, "/routes/:route_id/lunch/proposals", func() echo.HandlerFunc { return r.lunch }, Authenticate(r.runtime), AuthenticatedRateLimit(r.runtime)),
		router.NewRoute(http.MethodPost, "/routes/:route_id/apply", func() echo.HandlerFunc { return r.apply }, Authenticate(r.runtime), AuthenticatedRateLimit(r.runtime)),
		router.NewRoute(http.MethodPost, "/routes/:route_id/proposals/:proposal_id/reject", func() echo.HandlerFunc { return r.reject }, Authenticate(r.runtime), AuthenticatedRateLimit(r.runtime)),
	}
}

func (r *ProposalRouter) lunch(c *echo.Context) error {
	target, err := proposalTarget(c)
	if err != nil {
		return err
	}
	raw, err := proposalBody(c)
	if err != nil {
		return err
	}
	input, err := routewire.DecodeLunchProposalInput(raw)
	if err != nil {
		return malformedCommandHeader()
	}
	result, err := r.runtime.ProposeLunchChange(c.Request().Context(), target, input, RequestID(c))
	return sendProposalResult(c, result, err, false)
}

func (r *ProposalRouter) removal(c *echo.Context) error {
	target, err := visitTarget(c)
	if err != nil {
		return err
	}
	raw, err := proposalBody(c)
	if err != nil {
		return err
	}
	input, err := routewire.DecodeRemovalProposalInput(raw)
	if err != nil {
		return malformedCommandHeader()
	}
	result, err := r.runtime.ProposeVisitRemoval(c.Request().Context(), target, input, RequestID(c))
	return sendProposalResult(c, result, err, false)
}

func (r *ProposalRouter) apply(c *echo.Context) error {
	target, err := proposalTarget(c)
	if err != nil {
		return err
	}
	raw, err := proposalBody(c)
	if err != nil {
		return err
	}
	id, err := routewire.DecodeApplyProposalInput(raw)
	if err != nil {
		return malformedCommandHeader()
	}
	result, err := r.runtime.ApplyRouteProposal(c.Request().Context(), target, id)
	return sendProposalResult(c, result, err, true)
}

func (r *ProposalRouter) reject(c *echo.Context) error {
	target, err := proposalTarget(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c.Param("proposal_id"))
	if err != nil {
		return malformedCommandHeader()
	}
	if c.Request().Body != nil {
		raw, err := io.ReadAll(io.LimitReader(c.Request().Body, 1))
		if err != nil || len(raw) != 0 {
			return malformedCommandHeader()
		}
	}
	result, err := r.runtime.RejectRouteProposal(c.Request().Context(), target, d.ProposalID(id))
	return sendProposalResult(c, result, err, false)
}

func proposalTarget(c *echo.Context) (app.RouteCommand, error) {
	principal, ok := PrincipalFrom(c)
	if !ok {
		return app.RouteCommand{}, authRequired()
	}
	id, err := pathUUID(c.Param("route_id"))
	if err != nil {
		return app.RouteCommand{}, malformedCommandHeader()
	}
	key, err := ParseIdempotencyKey(c.Request())
	if err != nil {
		return app.RouteCommand{}, err
	}
	revision, err := ParseIfMatch(c.Request())
	if err != nil {
		return app.RouteCommand{}, err
	}
	return app.RouteCommand{ActorID: principal.UserID, RouteID: d.RouteID(id), ExpectedRevision: revision, Key: key}, nil
}

func proposalBody(c *echo.Context) ([]byte, error) {
	if c.Request().Body == nil {
		return nil, malformedCommandHeader()
	}
	raw, err := io.ReadAll(http.MaxBytesReader(c.Response(), c.Request().Body, maxVisitBodyBytes))
	if err != nil {
		return nil, malformedCommandHeader()
	}
	return raw, nil
}

func sendProposalResult(c *echo.Context, result command.Result, err error, apply bool) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	var provider *lunchprovider.Error
	switch {
	case errors.As(err, &provider):
		failure := &Error{Status: http.StatusServiceUnavailable, Code: "LUNCH_DETAILS_UNAVAILABLE", Message: "Lunch venue verification is unavailable", Retryable: provider.Retryable}
		switch provider.Kind {
		case "invalid_input":
			return malformedCommandHeader()
		case "not_food":
			failure.Status, failure.Code, failure.Message = http.StatusUnprocessableEntity, "LUNCH_VENUE_NOT_FOOD", "The organization is not a verified cafe"
		case "not_found":
			failure.Status, failure.Code = http.StatusNotFound, "LUNCH_ORGANIZATION_NOT_FOUND"
		case "access_denied":
			failure.Code = "LUNCH_DETAILS_ACCESS_DENIED"
		case "rate_limited":
			failure.Code = "LUNCH_DETAILS_RATE_LIMITED"
		case "busy":
			failure.Code = "LUNCH_DETAILS_BUSY"
		}
		return failure
	case errors.Is(err, routewire.ErrExternalCommitmentAcknowledgementRequired):
		return &Error{Status: http.StatusConflict, Code: "EXTERNAL_COMMITMENT_CONFIRMATION_REQUIRED", Message: "Confirm that removing the visit does not cancel the external booking"}
	case errors.Is(err, postgres.ErrProposalNotPending):
		return &Error{Status: http.StatusConflict, Code: "PROPOSAL_NOT_PENDING", Message: "Proposal is no longer pending"}
	case apply && errors.Is(err, postgres.ErrCatalogChanged):
		return &Error{Status: http.StatusConflict, Code: "PROPOSAL_CATALOG_CHANGED", Message: "Catalog changed; request a new proposal"}
	case errors.Is(err, routewire.ErrInvalidRecomputeInput):
		return &Error{Status: http.StatusUnprocessableEntity, Code: "INVALID_INPUT", Message: "Route state cannot be recomputed"}
	case err != nil:
		return mapRouteError(err)
	default:
		return sendVisitResult(c, &result)
	}
}
