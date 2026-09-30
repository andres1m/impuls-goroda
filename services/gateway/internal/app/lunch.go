package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/lunchprovider"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/optimizerclient"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/google/uuid"
)

func (r *Runtime) ProposeLunchChange(ctx context.Context, target RouteCommand, input routewire.LunchProposalInput, requestID string) (command.Result, error) {
	if r.queries == nil || r.commands == nil {
		return command.Result{}, errors.New("gateway commands are not initialized")
	}
	if target.RouteID == (d.RouteID{}) {
		return command.Result{}, optimizerclient.ErrInvalidInput
	}
	hash, err := command.Fingerprint(&command.FingerprintInput{ActorID: target.ActorID, Operation: command.ProposeLunchChange,
		Path: []command.PathComponent{{Name: "route_id", Value: idString(target.RouteID[:])}}, ExpectedRevision: &target.ExpectedRevision, Body: input})
	if err != nil {
		return command.Result{}, err
	}
	envelope := command.Envelope{ActorID: target.ActorID, Operation: command.ProposeLunchChange, Key: target.Key, RequestHash: hash}
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
	intent := routewire.LunchIntent{Input: input}
	if input.Action == "add" {
		id, err := uuid.NewV7()
		if err != nil {
			return command.Result{}, err
		}
		intent.NewID = d.VisitID(id)
	}
	if input.Venue != nil && input.Venue.Provider == "2gis" {
		if r.cfg.LunchProvider == nil {
			return command.Result{}, &lunchprovider.Error{Kind: "access_denied"}
		}
		organization, err := r.cfg.LunchProvider.Organization(ctx, input.Venue.ExternalID)
		if err != nil {
			return command.Result{}, err
		}
		intent.External = &d.ExternalVenueSnapshot{Provider: organization.Provider, ExternalID: organization.ExternalID,
			Title: organization.Title, Address: organization.Address, Position: organization.Position, ObservedAt: organization.ObservedAt,
			Price: d.Price{Status: d.PriceUnknown, Currency: "RUB"}, Availability: d.AvailabilityUnknown, HoursVerification: d.VerificationUnknown}
	}
	request, err := routewire.BuildLunchRecompute(target.RouteID, state.City, state.Plan, state.History, intent)
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
		computed, err := routewire.DecodeCheckedLunchRecompute(target.RouteID, state.City, state.Plan, state.History, intent, response)
		if err != nil {
			return command.Result{}, err
		}
		changes, err := routewire.ProposalChangesToWire(computed.Changes)
		if err != nil {
			return command.Result{}, err
		}
		result, err := r.commands.Execute(ctx, &envelope, func(q *postgres.Queries) (command.Result, error) {
			var proposal *routewire.PendingProposal
			if computed.Diagnostics.Status == "PROPOSED" {
				stored, err := q.SaveLunchProposal(ctx, state, intent, computed, r.clock().UTC())
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
			}{Status: computed.Diagnostics.Status, RouteID: idString(target.RouteID[:]), Revision: strconv.FormatInt(int64(state.Access.Revision), 10), Proposal: proposal,
				Changes: changes, Conflicts: append([]routewire.Conflict{}, computed.Diagnostics.Conflicts...)}
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
