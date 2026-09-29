package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/optimizerclient"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/jackc/pgx/v5"
)

type Optimizer interface {
	Optimize(context.Context, *pb.OptimizeRequest, string) (*pb.OptimizeResponse, error)
}

func (r *Runtime) OptimizeRoutes(ctx context.Context, actor d.UserID, key [16]byte, input routewire.ConfirmedRouteInput, requestID string) (command.Result, error) {
	if r.queries == nil || r.commands == nil {
		return command.Result{}, errors.New("gateway commands are not initialized")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return command.Result{}, optimizerclient.ErrInvalidInput
	}
	input, err = routewire.DecodeInput(raw)
	if err != nil {
		return command.Result{}, optimizerclient.ErrInvalidInput
	}
	hash, err := command.Fingerprint(&command.FingerprintInput{ActorID: actor, Operation: command.OptimizeRoutes, Body: input})
	if err != nil {
		return command.Result{}, err
	}
	envelope := command.Envelope{ActorID: actor, Operation: command.OptimizeRoutes, Key: key, RequestHash: hash}
	return r.computeRoutes(ctx, envelope, input, requestID, nil, nil)
}

func (r *Runtime) computeRoutes(ctx context.Context, envelope command.Envelope, input routewire.ConfirmedRouteInput, requestID string,
	before func(*postgres.Queries) error,
	after func(*postgres.Queries, []routewire.OwnerRoute, routewire.ResultDiagnostics) error,
) (command.Result, error) {
	if result, found, err := r.queries.LookupCommand(ctx, envelope); err != nil || found {
		return result, err
	}
	if r.cfg.Optimizer == nil {
		return command.Result{}, optimizerclient.ErrUnavailable
	}
	if err := r.queries.CheckCity(ctx, input.City, input.Timezone); err != nil {
		if errors.Is(err, postgres.ErrNotFound) || errors.Is(err, postgres.ErrCityTimezone) {
			return command.Result{}, optimizerclient.ErrInvalidInput
		}
		return command.Result{}, err
	}
	request, err := input.Proto()
	if err != nil {
		return command.Result{}, optimizerclient.ErrInvalidInput
	}
	for attempt := 0; attempt < 2; attempt++ {
		response, err := r.cfg.Optimizer.Optimize(ctx, request, requestID)
		if err != nil {
			return command.Result{}, err
		}
		computed, err := routewire.DecodeResult(input, response)
		if err != nil {
			return command.Result{}, err
		}
		result, err := r.commands.Execute(ctx, &envelope, func(q *postgres.Queries) (command.Result, error) {
			if before != nil {
				if err := before(q); err != nil {
					return command.Result{}, err
				}
			}
			if err := q.LockCatalog(ctx, input.City, computed.Diagnostics.CatalogRevision); err != nil {
				return command.Result{}, err
			}
			if err := q.CheckCity(ctx, input.City, input.Timezone); err != nil {
				return command.Result{}, err
			}
			for _, plan := range computed.Plans {
				if err := q.CheckPlanCatalog(ctx, input.City, plan); err != nil {
					return command.Result{}, err
				}
			}
			routes := make([]routewire.OwnerRoute, 0, len(computed.Plans))
			now := r.clock().UTC()
			for _, plan := range computed.Plans {
				id, err := q.CreateDraft(ctx, envelope.ActorID, input.City, plan, now)
				if err != nil {
					return command.Result{}, err
				}
				route, err := q.ReadOwnerRoute(ctx, id, envelope.ActorID)
				if err != nil {
					return command.Result{}, err
				}
				routes = append(routes, route)
			}
			diagnostics := computed.Diagnostics
			if after != nil {
				if err := after(q, routes, diagnostics); err != nil {
					return command.Result{}, err
				}
			}
			body := map[string]any{"status": diagnostics.Status, "routes": routes, "conflicts": diagnostics.Conflicts, "warnings": diagnostics.Warnings, "data_mode": diagnostics.DataMode, "computation_time_ms": diagnostics.ComputationTimeMS}
			if diagnostics.DataAsOf != nil {
				body["data_as_of"] = diagnostics.DataAsOf
			}
			template, err := json.Marshal(body)
			if err != nil {
				return command.Result{}, err
			}
			return command.Result{HTTPStatus: http.StatusOK, ResponseBody: template}, nil
		})
		if !errors.Is(err, postgres.ErrCatalogChanged) {
			return result, err
		}
	}
	return command.Result{}, postgres.ErrCatalogChanged
}

func (r *Runtime) ReadRoute(ctx context.Context, actor d.UserID, id d.RouteID) (routewire.OwnerRoute, error) {
	if r.queries == nil {
		return routewire.OwnerRoute{}, errors.New("gateway queries are not initialized")
	}
	if r.transactor == nil {
		return routewire.OwnerRoute{}, errors.New("gateway transactor is not initialized")
	}
	var route routewire.OwnerRoute
	err := r.transactor.WithinTx(ctx, &pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(q *postgres.Queries) error {
		var err error
		route, err = q.ReadOwnerRouteWithProposal(ctx, id, actor)
		return err
	})
	return route, err
}
