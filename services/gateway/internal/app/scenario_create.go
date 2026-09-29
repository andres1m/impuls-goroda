package app

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/google/uuid"
)

type ScenarioCreator struct {
	*Runtime
	extractor *ScenarioExtractor
}

func NewScenarioCreator(runtime *Runtime, extractor *ScenarioExtractor) *ScenarioCreator {
	return &ScenarioCreator{runtime, extractor}
}

func (r *ScenarioCreator) CreateScenario(ctx context.Context, owner d.UserID, key [16]byte, input routewire.CreateScenarioInput) (command.Result, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return command.Result{}, err
	}
	input, err = routewire.DecodeCreateScenarioInput(raw)
	if err != nil {
		return command.Result{}, err
	}
	hash, err := command.Fingerprint(&command.FingerprintInput{ActorID: owner, Operation: command.CreateScenario, Body: input})
	if err != nil {
		return command.Result{}, err
	}
	envelope := command.Envelope{ActorID: owner, Operation: command.CreateScenario, Key: key, RequestHash: hash}
	return r.commands.Execute(ctx, &envelope, func(q *postgres.Queries) (command.Result, error) {
		id, err := uuid.NewV7()
		if err != nil {
			return command.Result{}, err
		}
		scenario := routewire.BotScenario{ScenarioID: id.String(), Version: "1", Source: "custom", Status: "draft", UpdatedAt: r.clock().UTC()}
		if input.PresetID != "" {
			scenario.Source, scenario.PresetID = "preset", &input.PresetID
			scenario.Input, err = presetScenarioInput(input.PresetID)
			if err != nil {
				return command.Result{}, err
			}
		} else {
			scenario.SourceText = &input.SourceText
			scenario.PendingExtraction = r.extractor.Extract(ctx, input.SourceText)
		}
		if err := ctx.Err(); err != nil {
			return command.Result{}, err
		}
		if err := q.CreateBotScenario(ctx, owner, scenario); err != nil {
			return command.Result{}, err
		}
		body, err := json.Marshal(struct {
			Scenario routewire.BotScenario `json:"scenario"`
		}{scenario})
		return command.Result{HTTPStatus: http.StatusOK, ResponseBody: body}, err
	})
}
