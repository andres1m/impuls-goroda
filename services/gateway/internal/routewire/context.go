package routewire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type ScenarioContext struct {
	ConfirmedInput   *ConfirmedRouteInput `json:"confirmed_input"`
	SelectedRouteID  *string              `json:"selected_route_id,omitempty"`
	SelectedRevision *string              `json:"selected_revision,omitempty"`
	UpdatedAt        *time.Time           `json:"updated_at,omitempty"`
}

func DecodeScenarioSelection(raw []byte) (uuid.UUID, error) {
	invalid := errors.New("invalid scenario selection")
	if !utf8.Valid(raw) {
		return uuid.Nil, invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	value, err := readJSON(decoder, 0)
	if err != nil {
		return uuid.Nil, invalid
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return uuid.Nil, invalid
	}
	object, ok := value.(map[string]any)
	if !ok || len(object) != 1 {
		return uuid.Nil, invalid
	}
	text, ok := object["route_id"].(string)
	if !ok || len(text) != 36 {
		return uuid.Nil, invalid
	}
	id, err := uuid.Parse(text)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, invalid
	}
	return id, nil
}
