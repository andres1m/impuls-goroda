package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/sharewire"
	"github.com/jackc/pgx/v5"
)

type ShareCommand struct {
	ActorID          domain.UserID
	RouteID          domain.RouteID
	ExpectedRevision domain.RouteRevisionNumber
	Key              [16]byte
}

func (r *Runtime) ReadSharedRoute(ctx context.Context, token sharewire.Token) (sharewire.SharedRoute, error) {
	hash, err := token.Hash()
	if err != nil {
		return sharewire.SharedRoute{}, postgres.ErrNotFound
	}
	if r.db == nil || r.db.Pool == nil {
		return sharewire.SharedRoute{}, errors.New("gateway database is not initialized")
	}
	transactor, err := postgres.NewTransactor(r.db.Pool)
	if err != nil {
		return sharewire.SharedRoute{}, err
	}
	var result sharewire.SharedRoute
	err = transactor.WithinTx(ctx, &pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(q *postgres.Queries) error {
		var err error
		result, err = q.ReadSharedRoute(ctx, hash)
		return err
	})
	if err != nil {
		return sharewire.SharedRoute{}, err
	}
	return result, nil
}

type shareResultTemplate struct {
	Status    string     `json:"status"`
	Revision  string     `json:"revision"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

func (r *Runtime) CreateShare(ctx context.Context, target ShareCommand, input sharewire.CreateInput) (command.Result, error) {
	if r.commands == nil {
		return command.Result{}, errors.New("gateway commands are not initialized")
	}
	hash, err := input.Token.Hash()
	if err != nil {
		return command.Result{}, postgres.ErrInvalidShare
	}
	var expiry *time.Time
	if input.ExpiresAt != nil {
		value := input.ExpiresAt.UTC()
		expiry = &value
	}
	envelope, err := shareEnvelope(target, command.CreateRouteShare, struct {
		TokenHash [32]byte   `json:"token_hash"`
		ExpiresAt *time.Time `json:"expires_at,omitempty"`
	}{hash, expiry})
	if err != nil {
		return command.Result{}, err
	}
	return r.commands.Execute(ctx, &envelope, func(q *postgres.Queries) (command.Result, error) {
		mutation, err := q.CreateOwnedShare(ctx, target.RouteID, target.ActorID, target.ExpectedRevision,
			hash, expiry, r.clock().UTC())
		if err != nil {
			return command.Result{}, err
		}
		status := "READY"
		if mutation.Unchanged {
			status = "UNCHANGED"
		}
		body, err := json.Marshal(shareResultTemplate{Status: status,
			Revision:  strconv.FormatInt(int64(mutation.Revision), 10),
			CreatedAt: mutation.Share.CreatedAt, ExpiresAt: mutation.Share.ExpiresAt})
		if err != nil {
			return command.Result{}, err
		}
		return command.Result{HTTPStatus: http.StatusOK, ResponseBody: body,
			RouteID: &target.RouteID, ResultingRevision: &mutation.Revision}, nil
	})
}

func (r *Runtime) RevokeShare(ctx context.Context, target ShareCommand) (command.Result, error) {
	if r.commands == nil {
		return command.Result{}, errors.New("gateway commands are not initialized")
	}
	envelope, err := shareEnvelope(target, command.RevokeRouteShare, struct{}{})
	if err != nil {
		return command.Result{}, err
	}
	return r.commands.Execute(ctx, &envelope, func(q *postgres.Queries) (command.Result, error) {
		revision, err := q.RevokeOwnedShare(ctx, target.RouteID, target.ActorID, target.ExpectedRevision, r.clock().UTC())
		if err != nil {
			return command.Result{}, err
		}
		return command.Result{HTTPStatus: http.StatusNoContent, ResponseBody: json.RawMessage(`{}`),
			RouteID: &target.RouteID, ResultingRevision: &revision}, nil
	})
}

func shareEnvelope(target ShareCommand, operation command.Operation, body any) (command.Envelope, error) {
	if target.RouteID == (domain.RouteID{}) {
		return command.Envelope{}, postgres.ErrInvalidShare
	}
	hash, err := command.Fingerprint(&command.FingerprintInput{ActorID: target.ActorID, Operation: operation,
		Path:             []command.PathComponent{{Name: "route_id", Value: idString(target.RouteID[:])}},
		ExpectedRevision: &target.ExpectedRevision, Body: body})
	if err != nil {
		return command.Envelope{}, err
	}
	return command.Envelope{ActorID: target.ActorID, Operation: operation, Key: target.Key, RequestHash: hash}, nil
}
