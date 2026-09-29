package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/optimizerclient"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
)

func (r *Runtime) SaveBotScenarioDraft(ctx context.Context, owner d.UserID, scenarioID string, key [16]byte, input routewire.SaveBotScenarioDraftInput) (command.Result, error) {
	if r.commands == nil {
		return command.Result{}, errors.New("scenario runtime is not initialized")
	}
	if !routewire.ValidScenarioID(scenarioID) {
		return command.Result{}, postgres.ErrNotFound
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return command.Result{}, optimizerclient.ErrInvalidInput
	}
	input, err = routewire.DecodeSaveBotScenarioDraftInput(raw)
	if err != nil {
		return command.Result{}, optimizerclient.ErrInvalidInput
	}
	scenarioID = strings.ToLower(scenarioID)
	hash, err := command.Fingerprint(&command.FingerprintInput{ActorID: owner, Operation: command.SaveBotScenarioDraft,
		Path: []command.PathComponent{{Name: "scenario_id", Value: scenarioID}}, Body: input})
	if err != nil {
		return command.Result{}, err
	}
	envelope := command.Envelope{ActorID: owner, Operation: command.SaveBotScenarioDraft, Key: key, RequestHash: hash}
	return r.commands.Execute(ctx, &envelope, func(q *postgres.Queries) (command.Result, error) {
		scenario, err := q.LockBotScenario(ctx, owner, scenarioID, input.ExpectedVersion)
		if err != nil {
			return command.Result{}, err
		}
		scenario, err = q.SaveBotScenarioDraft(ctx, owner, scenario, input.Input, r.clock())
		if err != nil {
			return command.Result{}, err
		}
		body, err := json.Marshal(struct {
			Scenario routewire.BotScenario `json:"scenario"`
		}{scenario})
		return command.Result{HTTPStatus: http.StatusOK, ResponseBody: body}, err
	})
}
