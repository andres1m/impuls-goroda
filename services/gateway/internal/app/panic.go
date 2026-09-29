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

func (r *Runtime) ProposePanic(ctx context.Context, target RouteCommand, input routewire.PanicInput, requestID string) (command.Result, error) {
	if r.queries == nil || r.commands == nil {
		return command.Result{}, errors.New("gateway commands are not initialized")
	}
	if target.RouteID == (d.RouteID{}) || input.RouteID != idString(target.RouteID[:]) {
		return command.Result{}, optimizerclient.ErrInvalidInput
	}
	calculatedAt := r.clock().UTC()
	if _, err := input.Proto(calculatedAt); err != nil {
		return command.Result{}, optimizerclient.ErrInvalidInput
	}
	hash, err := command.Fingerprint(command.FingerprintInput{ActorID: target.ActorID, Operation: command.ProposePanicReroute, Path: []command.PathComponent{{Name: "route_id", Value: input.RouteID}}, ExpectedRevision: &target.ExpectedRevision, Body: input})
	if err != nil {
		return command.Result{}, err
	}
	envelope := command.Envelope{ActorID: target.ActorID, Operation: command.ProposePanicReroute, Key: target.Key, RequestHash: hash}
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
	request, err := routewire.BuildPanicRecompute(target.RouteID, state.City, state.Plan, state.History, input, calculatedAt)
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
		computed, err := routewire.DecodeCheckedPanicRecompute(target.RouteID, state.City, state.Plan, state.History, input, calculatedAt, response)
		if err != nil {
			return command.Result{}, err
		}
		changes, err := routewire.ProposalChangesToWire(computed.Changes)
		if err != nil {
			return command.Result{}, err
		}
		result, err := r.commands.Execute(ctx, envelope, func(q *postgres.Queries) (command.Result, error) {
			var proposal *routewire.PendingProposal
			if computed.Diagnostics.Status == "PROPOSED" {
				stored, err := q.SavePanicProposal(ctx, state, input, calculatedAt, computed, r.clock().UTC())
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
			}{computed.Diagnostics.Status, input.RouteID, strconv.FormatInt(int64(state.Access.Revision), 10), proposal, changes, append([]routewire.Conflict{}, computed.Diagnostics.Conflicts...)}
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
