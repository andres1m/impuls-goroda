package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/optimizerclient"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
)

type Recomputer interface {
	Recompute(context.Context, *pb.RecomputeRequest, string) (*pb.RecomputeResponse, error)
}

func (r *Runtime) SetVisitPin(ctx context.Context, target VisitCommand, input routewire.PinVisitInput, requestID string) (command.Result, error) {
	if r.queries == nil || r.commands == nil {
		return command.Result{}, errors.New("gateway commands are not initialized")
	}
	if target.RouteID == (d.RouteID{}) || target.VisitID == (d.VisitID{}) {
		return command.Result{}, optimizerclient.ErrInvalidInput
	}
	if _, err := input.Proto(target.VisitID); err != nil {
		return command.Result{}, optimizerclient.ErrInvalidInput
	}
	envelope, err := visitEnvelope(target, command.SetVisitPin, input)
	if err != nil {
		return command.Result{}, err
	}
	if result, found, err := r.queries.LookupCommand(ctx, envelope); err != nil || found {
		return result, err
	}
	state, err := r.queries.ReadRecomputeState(ctx, target.RouteID, target.ActorID)
	if err != nil {
		return command.Result{}, err
	}
	if err := postgres.RequireRevision(state.Access, target.ExpectedRevision); err != nil {
		return command.Result{}, err
	}
	found := false
	for _, step := range state.Plan.Steps {
		if step.VisitID == target.VisitID && step.Kind == d.VisitPlace {
			found = true
			break
		}
	}
	if !found {
		return command.Result{}, postgres.ErrNotFound
	}
	request, err := routewire.BuildPinRecompute(target.RouteID, state.City, state.Plan, state.History, target.VisitID, input)
	if err != nil {
		return command.Result{}, err
	}
	optimizer, ok := r.cfg.Optimizer.(Recomputer)
	if !ok || optimizer == nil {
		return command.Result{}, optimizerclient.ErrUnavailable
	}
	for attempt := 0; attempt < 2; attempt++ {
		if result, found, err := r.queries.LookupCommand(ctx, envelope); err != nil || found {
			return result, err
		}
		response, err := optimizer.Recompute(ctx, request, requestID)
		if err != nil {
			return command.Result{}, err
		}
		computed, err := routewire.DecodeCheckedPinRecompute(target.RouteID, state.City, state.Plan, state.History, target.VisitID, input, response)
		if err != nil {
			return command.Result{}, err
		}
		result, err := r.commands.Execute(ctx, envelope, func(q *postgres.Queries) (command.Result, error) {
			access, err := q.LockOwnedRoute(ctx, target.RouteID, target.ActorID)
			if err != nil {
				return command.Result{}, err
			}
			if err := postgres.RequireRevision(access, target.ExpectedRevision); err != nil {
				return command.Result{}, err
			}
			if access.Lifecycle != state.Access.Lifecycle {
				return command.Result{}, routewire.ErrInvalidResult
			}
			if err := q.LockCatalog(ctx, state.City, computed.Diagnostics.CatalogRevision); err != nil {
				return command.Result{}, err
			}
			if err := q.CheckCity(ctx, state.City, state.Plan.Timezone); err != nil {
				return command.Result{}, err
			}
			revision, status := access.Revision, computed.Diagnostics.Status
			if status == "PROPOSED" {
				revision, err = q.SavePinRevision(ctx, access, state.City, state.Plan, state.History, target.VisitID, input, computed, r.clock().UTC())
				if err != nil {
					return command.Result{}, err
				}
				status = string(computed.Candidate.Result)
			}
			conflicts := append([]routewire.Conflict{}, computed.Diagnostics.Conflicts...)
			body := struct {
				Status    string                `json:"status"`
				RouteID   string                `json:"route_id"`
				Revision  string                `json:"revision"`
				Route     *routewire.OwnerRoute `json:"route,omitempty"`
				Conflicts []routewire.Conflict  `json:"conflicts"`
			}{Status: status, RouteID: idString(target.RouteID[:]), Revision: strconv.FormatInt(int64(revision), 10), Conflicts: conflicts}
			if status != "CONFLICT" {
				route, err := q.ReadOwnerRoute(ctx, target.RouteID, target.ActorID)
				if err != nil {
					return command.Result{}, err
				}
				body.Route = &route
			}
			encoded, err := json.Marshal(body)
			if err != nil {
				return command.Result{}, err
			}
			return command.Result{HTTPStatus: http.StatusOK, ResponseBody: encoded, RouteID: &target.RouteID, ResultingRevision: &revision}, nil
		})
		if !errors.Is(err, postgres.ErrCatalogChanged) {
			return result, err
		}
	}
	return command.Result{}, postgres.ErrCatalogChanged
}
