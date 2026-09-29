package routewire

import "encoding/json"

type SaveBotScenarioDraftInput struct {
	ExpectedVersion int64            `json:"expected_version,string"`
	Input           BotScenarioInput `json:"input"`
}

func DecodeSaveBotScenarioDraftInput(raw []byte) (SaveBotScenarioDraftInput, error) {
	var input SaveBotScenarioDraftInput
	if err := decodeScenarioJSON(raw, "SaveBotScenarioDraftRequest", &input); err != nil || input.ExpectedVersion < 1 {
		return SaveBotScenarioDraftInput{}, ErrInvalidScenarioInput
	}
	encoded, err := json.Marshal(input.Input)
	if err != nil {
		return SaveBotScenarioDraftInput{}, ErrInvalidScenarioInput
	}
	input.Input, err = DecodeBotScenarioInput(encoded)
	return input, err
}
