package routewire

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

func DecodeBotScenario(raw []byte) (BotScenario, error) {
	var scenario BotScenario
	if err := decodeScenarioJSON(raw, "BotScenario", &scenario); err != nil {
		return BotScenario{}, err
	}
	version, err := strconv.ParseInt(scenario.Version, 10, 64)
	if err != nil || version < 1 || validInstant(scenario.UpdatedAt) != nil {
		return BotScenario{}, ErrInvalidScenarioInput
	}
	if scenario.Source == "preset" && scenario.PresetID == nil ||
		scenario.Source == "custom" && (scenario.SourceText == nil || strings.TrimSpace(*scenario.SourceText) == "") {
		return BotScenario{}, ErrInvalidScenarioInput
	}
	if scenario.Input.validateKnownFields() != nil ||
		scenario.PendingExtraction != nil && scenario.PendingExtraction.validateKnownFields() != nil {
		return BotScenario{}, ErrInvalidScenarioInput
	}
	if scenario.Status == "completed" && (scenario.PendingExtraction != nil || scenario.Outcome == nil) {
		return BotScenario{}, ErrInvalidScenarioInput
	}
	if outcome := scenario.Outcome; outcome != nil {
		success := outcome.Status == "READY" || outcome.Status == "PARTIAL"
		if success != (scenario.Status == "completed") || success && (len(outcome.RouteIDs) == 0 || len(outcome.Conflicts) != 0) ||
			!success && (len(outcome.RouteIDs) != 0 || len(outcome.Conflicts)+len(outcome.Warnings) == 0) ||
			outcome.Status == "CONFLICT" && len(outcome.Conflicts) == 0 {
			return BotScenario{}, ErrInvalidScenarioInput
		}
		if outcome.DataAsOf != nil && validInstant(*outcome.DataAsOf) != nil {
			return BotScenario{}, ErrInvalidScenarioInput
		}
		for _, warning := range outcome.Warnings {
			if !validExplanation(warning.Code, warning.Message) {
				return BotScenario{}, ErrInvalidScenarioInput
			}
			switch warning.Scope {
			case "route":
				if warning.VisitID != nil || warning.LegPosition != nil {
					return BotScenario{}, ErrInvalidScenarioInput
				}
			case "visit":
				if warning.VisitID == nil || warning.LegPosition != nil {
					return BotScenario{}, ErrInvalidScenarioInput
				}
			case "leg":
				if warning.VisitID != nil || warning.LegPosition == nil {
					return BotScenario{}, ErrInvalidScenarioInput
				}
			}
		}
		for _, conflict := range outcome.Conflicts {
			if !validExplanation(conflict.Code, conflict.Message) {
				return BotScenario{}, ErrInvalidScenarioInput
			}
		}
		full, err := json.Marshal(scenario.Input)
		if err != nil {
			return BotScenario{}, ErrInvalidScenarioInput
		}
		if _, err := DecodeInput(full); err != nil {
			return BotScenario{}, ErrInvalidScenarioInput
		}
	}
	return scenario, nil
}

func ValidScenarioID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id.Version() == 7 && id.Variant() == uuid.RFC4122 && strings.EqualFold(id.String(), value)
}
