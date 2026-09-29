package routewire

import "encoding/json"

type botScenarioEnvelope struct {
	FormatVersion string      `json:"format_version"`
	Scenario      BotScenario `json:"scenario"`
}

func EncodeBotScenarioState(scenario BotScenario) ([]byte, error) {
	raw, err := json.Marshal(scenario)
	if err != nil {
		return nil, ErrInvalidScenarioInput
	}
	if _, err := DecodeBotScenario(raw); err != nil {
		return nil, err
	}
	return json.Marshal(botScenarioEnvelope{FormatVersion: "1", Scenario: scenario})
}

func DecodeBotScenarioState(raw []byte) (BotScenario, error) {
	var envelope botScenarioEnvelope
	if err := decodeScenarioJSON(raw, "BotScenarioEnvelope", &envelope); err != nil || envelope.FormatVersion != "1" {
		return BotScenario{}, ErrInvalidScenarioInput
	}
	encoded, err := json.Marshal(envelope.Scenario)
	if err != nil {
		return BotScenario{}, ErrInvalidScenarioInput
	}
	return DecodeBotScenario(encoded)
}
