package routewire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/google/uuid"
)

func DecodeApplyProposalInput(raw []byte) (d.ProposalID, error) {
	if !utf8.Valid(raw) {
		return d.ProposalID{}, errors.New("invalid proposal encoding")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := readJSON(decoder, 0)
	if err != nil {
		return d.ProposalID{}, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return d.ProposalID{}, errors.New("unexpected trailing proposal JSON")
	}
	fields, ok := value.(map[string]any)
	if !ok || len(fields) != 1 {
		return d.ProposalID{}, errors.New("invalid proposal fields")
	}
	text, ok := fields["proposal_id"].(string)
	if !ok {
		return d.ProposalID{}, errors.New("proposal identifier is required")
	}
	id, err := uuid.Parse(text)
	if err != nil || id == uuid.Nil || len(text) != 36 {
		return d.ProposalID{}, errors.New("invalid proposal identifier")
	}
	return d.ProposalID(id), nil
}
