package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/optimizerclient"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
)

func (r *Runtime) ProposeVisitRemoval(ctx context.Context, target VisitCommand, input routewire.RemovalProposalInput, requestID string) (command.Result, error) {
	if r.queries == nil || r.commands == nil {
		return command.Result{}, errors.New("gateway commands are not initialized")
	}
	if target.RouteID == (d.RouteID{}) || target.VisitID == (d.VisitID{}) {
		return command.Result{}, optimizerclient.ErrInvalidInput
	}
	if _, err := input.Proto(target.VisitID); err != nil {
		return command.Result{}, optimizerclient.ErrInvalidInput
	}
	envelope, err := visitEnvelope(target, command.ProposeVisitRemoval, input)
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
		if step.VisitID == target.VisitID {
			found = true
			break
		}
	}
	if !found {
		return command.Result{}, postgres.ErrNotFound
	}
	request, err := routewire.BuildRemovalRecompute(target.RouteID, state.City, state.Plan, state.History, target.VisitID, input)
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
		computed, err := routewire.DecodeCheckedRemovalRecompute(target.RouteID, state.City, state.Plan, state.History, target.VisitID, input, response)
		if err != nil {
			return command.Result{}, err
		}
		if computed.Diagnostics.Status == "PROPOSED" {
			for _, step := range state.Plan.Steps {
				if step.VisitID == target.VisitID && (step.Participation.Status == d.ParticipationUserReported || step.Participation.Status == d.ParticipationProviderConfirmed) {
					action := "review_external_booking"
					id := target.VisitID
					computed.Changes = append(computed.Changes, routewire.RecomputedChange{Kind: "participation_action", Scope: d.WarningVisit, BeforeVisitID: &id, ParticipationAction: &action, Message: "Removing this visit does not cancel the external ticket or registration. Check the booking with the organiser."})
					break
				}
			}
		}
		changes, err := routewire.ProposalChangesToWire(computed.Changes)
		if err != nil {
			return command.Result{}, err
		}
		result, err := r.commands.Execute(ctx, envelope, func(q *postgres.Queries) (command.Result, error) {
			var proposal *routewire.PendingProposal
			if computed.Diagnostics.Status == "PROPOSED" {
				stored, err := q.SaveRemovalProposal(ctx, state, target.VisitID, input, computed, r.clock().UTC())
				if err != nil {
					return command.Result{}, err
				}
				proposal = &stored
			} else if _, err := q.CheckRecomputeBasis(ctx, state, computed.Diagnostics.CatalogRevision); err != nil {
				return command.Result{}, err
			}
			body := struct {
				Status    string                     `json:"status"`
				RouteID   string                     `json:"route_id"`
				Revision  string                     `json:"revision"`
				Proposal  *routewire.PendingProposal `json:"proposal,omitempty"`
				Changes   []routewire.ProposalChange `json:"changes"`
				Conflicts []routewire.Conflict       `json:"conflicts"`
			}{Status: computed.Diagnostics.Status, RouteID: idString(target.RouteID[:]), Revision: strconv.FormatInt(int64(state.Access.Revision), 10), Proposal: proposal, Changes: changes, Conflicts: append([]routewire.Conflict{}, computed.Diagnostics.Conflicts...)}
			encoded, err := json.Marshal(body)
			if err != nil {
				return command.Result{}, err
			}
			revision := state.Access.Revision
			return command.Result{HTTPStatus: http.StatusOK, ResponseBody: encoded, RouteID: &target.RouteID, ResultingRevision: &revision}, nil
		})
		if !errors.Is(err, postgres.ErrCatalogChanged) {
			return result, err
		}
	}
	return command.Result{}, postgres.ErrCatalogChanged
}
