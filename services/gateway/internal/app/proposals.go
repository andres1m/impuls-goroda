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

func (r *Runtime) ApplyRouteProposal(ctx context.Context, target RouteCommand, proposalID d.ProposalID) (command.Result, error) {
	return r.resolveRouteProposal(ctx, target, proposalID, true)
}

func (r *Runtime) RejectRouteProposal(ctx context.Context, target RouteCommand, proposalID d.ProposalID) (command.Result, error) {
	return r.resolveRouteProposal(ctx, target, proposalID, false)
}

func (r *Runtime) resolveRouteProposal(ctx context.Context, target RouteCommand, proposalID d.ProposalID, apply bool) (command.Result, error) {
	if r.commands == nil {
		return command.Result{}, errors.New("gateway commands are not initialized")
	}
	if target.RouteID == (d.RouteID{}) || proposalID == (d.ProposalID{}) {
		return command.Result{}, optimizerclient.ErrInvalidInput
	}
	operation := command.RejectRouteProposal
	path := []command.PathComponent{{Name: "route_id", Value: idString(target.RouteID[:])}}
	var body any = struct{}{}
	if apply {
		operation = command.ApplyRouteProposal
		body = struct {
			ProposalID string `json:"proposal_id"`
		}{idString(proposalID[:])}
	} else {
		path = append(path, command.PathComponent{Name: "proposal_id", Value: idString(proposalID[:])})
	}
	hash, err := command.Fingerprint(&command.FingerprintInput{ActorID: target.ActorID, Operation: operation, Path: path, ExpectedRevision: &target.ExpectedRevision, Body: body})
	if err != nil {
		return command.Result{}, err
	}
	envelope := command.Envelope{ActorID: target.ActorID, Operation: operation, Key: target.Key, RequestHash: hash}
	return r.commands.Execute(ctx, &envelope, func(q *postgres.Queries) (command.Result, error) {
		var revision d.RouteRevisionNumber
		reason, err := q.ReadProposalReason(ctx, target.RouteID, target.ActorID, proposalID)
		if err != nil {
			return command.Result{}, err
		}
		switch reason {
		case "cancel":
			if apply {
				revision, err = q.ApplyCancellationProposal(ctx, target.RouteID, target.ActorID, target.ExpectedRevision, proposalID, r.clock().UTC())
			} else {
				revision, err = q.RejectCancellationProposal(ctx, target.RouteID, target.ActorID, target.ExpectedRevision, proposalID, r.clock().UTC())
			}
		case "delete":
			if apply {
				revision, err = q.ApplyRemovalProposal(ctx, target.RouteID, target.ActorID, target.ExpectedRevision, proposalID, r.clock().UTC())
			} else {
				revision, err = q.RejectRemovalProposal(ctx, target.RouteID, target.ActorID, target.ExpectedRevision, proposalID, r.clock().UTC())
			}
		case "delay":
			if apply {
				revision, err = q.ApplyPanicProposal(ctx, target.RouteID, target.ActorID, target.ExpectedRevision, proposalID, r.clock().UTC())
			} else {
				revision, err = q.RejectPanicProposal(ctx, target.RouteID, target.ActorID, target.ExpectedRevision, proposalID, r.clock().UTC())
			}
		default:
			return command.Result{}, routewire.ErrInvalidResult
		}
		if err != nil {
			return command.Result{}, err
		}
		response := map[string]any{"status": "UNCHANGED", "route_id": idString(target.RouteID[:]), "revision": strconv.FormatInt(int64(revision), 10), "conflicts": []routewire.Conflict{}}
		if apply {
			route, err := q.ReadOwnerRoute(ctx, target.RouteID, target.ActorID)
			if err != nil {
				return command.Result{}, err
			}
			response["status"] = route.Plan.Result
			response["route"] = route
		} else {
			response["changes"] = []routewire.ProposalChange{}
		}
		encoded, err := json.Marshal(response)
		if err != nil {
			return command.Result{}, err
		}
		return command.Result{HTTPStatus: http.StatusOK, ResponseBody: encoded, RouteID: &target.RouteID, ResultingRevision: &revision}, nil
	})
}
