package routewire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

type DeleteRouteInput struct {
	AcknowledgeExternalCommitments bool `json:"acknowledge_external_commitments"`
}

func DecodeDeleteRouteInput(raw []byte) (DeleteRouteInput, error) {
	if !utf8.Valid(raw) {
		return DeleteRouteInput{}, errors.New("invalid deletion input")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	value, err := readJSON(decoder, 0)
	if err != nil {
		return DeleteRouteInput{}, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return DeleteRouteInput{}, errors.New("unexpected trailing deletion input")
	}
	fields, ok := value.(map[string]any)
	if !ok || len(fields) != 1 {
		return DeleteRouteInput{}, errors.New("invalid deletion fields")
	}
	acknowledge, ok := fields["acknowledge_external_commitments"].(bool)
	if !ok {
		return DeleteRouteInput{}, errors.New("invalid deletion acknowledgement")
	}
	return DeleteRouteInput{AcknowledgeExternalCommitments: acknowledge}, nil
}
