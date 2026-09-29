package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/optimizerclient"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
)

func (r *Runtime) ReadBotScenario(ctx context.Context, owner d.UserID, scenarioID string) (routewire.BotScenario, error) {
	if r.queries == nil {
		return routewire.BotScenario{}, errors.New("scenario runtime is not initialized")
	}
	return r.queries.ReadBotScenario(ctx, owner, scenarioID)
}

func (r *Runtime) CompleteBotScenario(ctx context.Context, owner d.UserID, scenarioID string, key [16]byte, input routewire.CompleteBotScenarioInput, requestID string) (command.Result, error) {
	if r.queries == nil || r.commands == nil {
		return command.Result{}, errors.New("scenario runtime is not initialized")
	}
	if !routewire.ValidScenarioID(scenarioID) {
		return command.Result{}, postgres.ErrNotFound
	}
	scenarioID = strings.ToLower(scenarioID)
	raw, err := json.Marshal(input)
	if err != nil {
		return command.Result{}, optimizerclient.ErrInvalidInput
	}
	input, err = routewire.DecodeCompleteBotScenarioInput(raw)
	if err != nil {
		return command.Result{}, optimizerclient.ErrInvalidInput
	}
	hash, err := command.Fingerprint(&command.FingerprintInput{
		ActorID: owner, Operation: command.CompleteBotScenario,
		Path: []command.PathComponent{{Name: "scenario_id", Value: scenarioID}}, Body: input,
	})
	if err != nil {
		return command.Result{}, err
	}
	envelope := command.Envelope{ActorID: owner, Operation: command.CompleteBotScenario, Key: key, RequestHash: hash}
	if result, found, err := r.queries.LookupCommand(ctx, envelope); err != nil || found {
		return result, err
	}
	scenario, err := r.queries.ReadBotScenario(ctx, owner, scenarioID)
	if err == nil {
		err = postgres.RequireScenarioVersion(scenario, input.ExpectedVersion)
	}
	if err != nil {
		if result, found, replayErr := r.queries.LookupCommand(ctx, envelope); replayErr != nil || found {
			return result, replayErr
		}
		return command.Result{}, err
	}
	return r.computeRoutes(ctx, envelope, input.Input, requestID,
		func(q *postgres.Queries) error {
			var err error
			scenario, err = q.LockBotScenario(ctx, owner, scenarioID, input.ExpectedVersion)
			return err
		},
		func(q *postgres.Queries, routes []routewire.OwnerRoute, diagnostics routewire.ResultDiagnostics) error {
			ids := make([]string, 0, len(routes))
			for _, route := range routes {
				ids = append(ids, route.RouteID)
			}
			return q.RecordScenarioOutcome(ctx, owner, scenario, input.Input, routewire.BotScenarioOutcome{
				Status: diagnostics.Status, RouteIDs: ids, Warnings: diagnostics.Warnings,
				Conflicts: diagnostics.Conflicts, DataMode: diagnostics.DataMode, DataAsOf: diagnostics.DataAsOf,
			}, r.clock())
		})
}

func (r *Runtime) ReadLatestBotScenario(ctx context.Context, owner d.UserID) (*routewire.BotScenario, error) {
	if r.queries == nil {
		return nil, errors.New("scenario runtime is not initialized")
	}
	return r.queries.ReadLatestBotScenario(ctx, owner)
}
