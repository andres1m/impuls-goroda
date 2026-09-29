package routewire

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"
)

type CreateScenarioInput struct {
	PresetID   string `json:"preset_id,omitempty"`
	SourceText string `json:"source_text,omitempty"`
}

func DecodeCreateScenarioInput(raw []byte) (CreateScenarioInput, error) {
	var input CreateScenarioInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	value, err := readJSON(decoder, 0)
	fields, ok := value.(map[string]any)
	if !utf8.Valid(raw) || err != nil || !ok || len(fields) != 1 || decoder.Decode(new(any)) != io.EOF {
		return input, ErrInvalidScenarioInput
	}
	for name, value := range fields {
		text, ok := value.(string)
		if !ok {
			return input, ErrInvalidScenarioInput
		}
		switch name {
		case "preset_id":
			input.PresetID = text
		case "source_text":
			input.SourceText = text
		default:
			return input, ErrInvalidScenarioInput
		}
	}
	input.SourceText = strings.TrimSpace(input.SourceText)
	if (input.PresetID == "") == (input.SourceText == "") || utf8.RuneCountInString(input.SourceText) > 4000 {
		return input, ErrInvalidScenarioInput
	}
	if input.PresetID != "" {
		switch input.PresetID {
		case "vibe", "mood", "culture", "energy", "balance", "benefit":
		default:
			return input, ErrInvalidScenarioInput
		}
	}
	return input, nil
}
