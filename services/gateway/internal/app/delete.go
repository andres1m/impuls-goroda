package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
)

func (r *Runtime) DeleteRoute(ctx context.Context, target RouteCommand, input routewire.DeleteRouteInput) (command.Result, error) {
	if r.commands == nil || target.RouteID == (domain.RouteID{}) {
		return command.Result{}, errors.New("route deletion is not initialized")
	}
	hash, err := command.Fingerprint(command.FingerprintInput{ActorID: target.ActorID, Operation: command.DeleteRoute,
		Path:             []command.PathComponent{{Name: "route_id", Value: idString(target.RouteID[:])}},
		ExpectedRevision: &target.ExpectedRevision, Body: input})
	if err != nil {
		return command.Result{}, err
	}
	return r.commands.Execute(ctx, command.Envelope{ActorID: target.ActorID, Operation: command.DeleteRoute, Key: target.Key, RequestHash: hash}, func(q *postgres.Queries) (command.Result, error) {
		if err := q.DeleteOwnedRoute(ctx, target.RouteID, target.ActorID, target.ExpectedRevision, input.AcknowledgeExternalCommitments); err != nil {
			return command.Result{}, err
		}
		return command.Result{HTTPStatus: http.StatusNoContent, ResponseBody: json.RawMessage(`{}`), RouteID: &target.RouteID}, nil
	})
}
