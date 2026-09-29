package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
)

type ScenarioSelection struct {
	ActorID          d.UserID
	RouteID          d.RouteID
	ExpectedRevision d.RouteRevisionNumber
	Key              [16]byte
}

func (r *Runtime) ReadScenarioContext(ctx context.Context, actor d.UserID) (routewire.ScenarioContext, error) {
	if r.queries == nil {
		return routewire.ScenarioContext{}, errors.New("gateway queries are not initialized")
	}
	return r.queries.ReadScenarioContext(ctx, actor)
}

func (r *Runtime) SelectScenarioRoute(ctx context.Context, target ScenarioSelection) (command.Result, error) {
	if r.commands == nil || target.RouteID == (d.RouteID{}) {
		return command.Result{}, errors.New("scenario command is not initialized")
	}
	body := struct {
		RouteID string `json:"route_id"`
	}{idString(target.RouteID[:])}
	hash, err := command.Fingerprint(command.FingerprintInput{
		ActorID: target.ActorID, Operation: command.SelectScenarioRoute,
		ExpectedRevision: &target.ExpectedRevision, Body: body,
	})
	if err != nil {
		return command.Result{}, err
	}
	envelope := command.Envelope{ActorID: target.ActorID, Operation: command.SelectScenarioRoute, Key: target.Key, RequestHash: hash}
	return r.commands.Execute(ctx, envelope, func(q *postgres.Queries) (command.Result, error) {
		access, err := q.LockOwnedRoute(ctx, target.RouteID, target.ActorID)
		if err != nil {
			return command.Result{}, err
		}
		if err := postgres.RequireRevision(access, target.ExpectedRevision); err != nil {
			return command.Result{}, err
		}
		if err := q.SelectScenarioRoute(ctx, access); err != nil {
			return command.Result{}, err
		}
		response, err := json.Marshal(struct {
			Status    string     `json:"status"`
			RouteID   string     `json:"route_id"`
			Revision  string     `json:"revision"`
			Conflicts []struct{} `json:"conflicts"`
		}{"READY", body.RouteID, strconv.FormatInt(int64(access.Revision), 10), []struct{}{}})
		if err != nil {
			return command.Result{}, err
		}
		return command.Result{HTTPStatus: http.StatusOK, ResponseBody: response, RouteID: &target.RouteID, ResultingRevision: &access.Revision}, nil
	})
}
