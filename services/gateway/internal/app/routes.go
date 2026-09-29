package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
)

type RouteCommand struct {
	ActorID          domain.UserID
	RouteID          domain.RouteID
	ExpectedRevision domain.RouteRevisionNumber
	Key              [16]byte
}

func (r *Runtime) SaveRoute(ctx context.Context, target RouteCommand) (command.Result, error) {
	if r.commands == nil {
		return command.Result{}, errors.New("gateway commands are not initialized")
	}
	if target.RouteID == (domain.RouteID{}) {
		return command.Result{}, errors.New("route identifier is required")
	}
	hash, err := command.Fingerprint(command.FingerprintInput{
		ActorID: target.ActorID, Operation: command.SaveRoute,
		Path:             []command.PathComponent{{Name: "route_id", Value: idString(target.RouteID[:])}},
		ExpectedRevision: &target.ExpectedRevision, Body: struct{}{},
	})
	if err != nil {
		return command.Result{}, err
	}
	envelope := command.Envelope{ActorID: target.ActorID, Operation: command.SaveRoute, Key: target.Key, RequestHash: hash}
	return r.commands.Execute(ctx, envelope, func(q *postgres.Queries) (command.Result, error) {
		access, err := q.LockOwnedRoute(ctx, target.RouteID, target.ActorID)
		if err != nil {
			return command.Result{}, err
		}
		if err := postgres.RequireRevision(access, target.ExpectedRevision); err != nil {
			return command.Result{}, err
		}
		status, revision := "UNCHANGED", access.Revision
		if access.Lifecycle == domain.RouteDraft {
			revision, err = q.SaveDraft(ctx, access, r.clock().UTC())
			if err != nil {
				return command.Result{}, err
			}
			status = "READY"
		}
		body, err := json.Marshal(struct {
			Status    string     `json:"status"`
			RouteID   string     `json:"route_id"`
			Revision  string     `json:"revision"`
			Conflicts []struct{} `json:"conflicts"`
		}{status, idString(target.RouteID[:]), strconv.FormatInt(int64(revision), 10), []struct{}{}})
		if err != nil {
			return command.Result{}, err
		}
		return command.Result{HTTPStatus: http.StatusOK, ResponseBody: body, RouteID: &target.RouteID, ResultingRevision: &revision}, nil
	})
}
