package routewire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
)

type PinVisitInput struct {
	PinKind string `json:"pin_kind"`
}

func DecodePinVisitInput(raw []byte) (PinVisitInput, error) {
	if !utf8.Valid(raw) {
		return PinVisitInput{}, errors.New("invalid pin encoding")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := readJSON(decoder, 0)
	if err != nil {
		return PinVisitInput{}, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return PinVisitInput{}, errors.New("unexpected trailing pin JSON")
	}
	fields, ok := value.(map[string]any)
	if !ok || len(fields) != 1 {
		return PinVisitInput{}, errors.New("invalid pin fields")
	}
	kind, ok := fields["pin_kind"].(string)
	if !ok {
		return PinVisitInput{}, errors.New("invalid pin kind")
	}
	input := PinVisitInput{PinKind: kind}
	if _, err := input.protoKind(); err != nil {
		return PinVisitInput{}, err
	}
	return input, nil
}

func (in PinVisitInput) protoKind() (pb.PinKind, error) {
	switch in.PinKind {
	case "preferred":
		return pb.PinKind_PIN_KIND_PREFERRED, nil
	case "obligation":
		return pb.PinKind_PIN_KIND_OBLIGATION, nil
	case "none":
		return pb.PinKind_PIN_KIND_NONE, nil
	default:
		return pb.PinKind_PIN_KIND_UNSPECIFIED, errors.New("invalid pin kind")
	}
}

func (in PinVisitInput) Proto(visitID d.VisitID) (*pb.PinTrigger, error) {
	if visitID == (d.VisitID{}) {
		return nil, errors.New("pin visit identifier is required")
	}
	kind, err := in.protoKind()
	if err != nil {
		return nil, err
	}
	id := visitID
	return &pb.PinTrigger{VisitId: append([]byte(nil), id[:]...), Kind: kind}, nil
}
