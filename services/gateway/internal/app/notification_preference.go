package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
)

type NotificationPreferenceCommand struct {
	ActorID          d.UserID
	RouteID          d.RouteID
	ExpectedRevision d.RouteRevisionNumber
	Key              [16]byte
	Input            routewire.NotificationPreferenceInput
}

func (r *Runtime) ReadNotificationPreference(ctx context.Context, actor d.UserID, routeID d.RouteID) (routewire.NotificationPreferenceResponse, error) {
	if r.queries == nil {
		return routewire.NotificationPreferenceResponse{}, errors.New("gateway queries are not initialized")
	}
	return r.queries.ReadNotificationPreference(ctx, routeID, actor)
}

func (r *Runtime) SetNotificationPreference(ctx context.Context, target NotificationPreferenceCommand) (command.Result, error) {
	if r.commands == nil || target.RouteID == (d.RouteID{}) {
		return command.Result{}, errors.New("notification command is not initialized")
	}
	body := struct {
		RouteID string `json:"route_id"`
		routewire.NotificationPreferenceInput
	}{idString(target.RouteID[:]), target.Input}
	hash, err := command.Fingerprint(&command.FingerprintInput{
		ActorID: target.ActorID, Operation: command.SetRouteNotifications,
		ExpectedRevision: &target.ExpectedRevision, Body: body,
	})
	if err != nil {
		return command.Result{}, err
	}
	envelope := command.Envelope{ActorID: target.ActorID, Operation: command.SetRouteNotifications, Key: target.Key, RequestHash: hash}
	return r.commands.Execute(ctx, &envelope, func(q *postgres.Queries) (command.Result, error) {
		access, err := q.LockOwnedRoute(ctx, target.RouteID, target.ActorID)
		if err != nil {
			return command.Result{}, err
		}
		if err := postgres.RequireRevision(access, target.ExpectedRevision); err != nil {
			return command.Result{}, err
		}
		preference, err := q.SetNotificationPreference(ctx, access, target.Input, r.clock().UTC())
		if err != nil {
			return command.Result{}, err
		}
		raw, err := json.Marshal(preference)
		if err != nil {
			return command.Result{}, err
		}
		return command.Result{HTTPStatus: http.StatusOK, ResponseBody: raw, RouteID: &target.RouteID, ResultingRevision: &access.Revision}, nil
	})
}
