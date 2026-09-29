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

var ErrExternalCommitmentAcknowledgementRequired = errors.New("external commitment acknowledgement is required")

type RemovalProposalInput struct {
	Mode                          string `json:"mode"`
	AcknowledgeExternalCommitment bool   `json:"acknowledge_external_commitment"`
}

func DecodeRemovalProposalInput(raw []byte) (RemovalProposalInput, error) {
	if !utf8.Valid(raw) {
		return RemovalProposalInput{}, errors.New("invalid removal encoding")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := readJSON(decoder, 0)
	if err != nil {
		return RemovalProposalInput{}, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return RemovalProposalInput{}, errors.New("unexpected trailing removal JSON")
	}
	fields, ok := value.(map[string]any)
	if !ok || len(fields) != 2 {
		return RemovalProposalInput{}, errors.New("invalid removal fields")
	}
	mode, ok := fields["mode"].(string)
	if !ok {
		return RemovalProposalInput{}, errors.New("invalid removal mode")
	}
	acknowledge, ok := fields["acknowledge_external_commitment"].(bool)
	if !ok {
		return RemovalProposalInput{}, errors.New("removal acknowledgement is required")
	}
	input := RemovalProposalInput{Mode: mode, AcknowledgeExternalCommitment: acknowledge}
	if _, err := input.protoMode(); err != nil {
		return RemovalProposalInput{}, err
	}
	return input, nil
}

func (in RemovalProposalInput) protoMode() (pb.RemovalMode, error) {
	switch in.Mode {
	case "rebuild":
		return pb.RemovalMode_REMOVAL_MODE_REBUILD, nil
	case "free_time":
		return pb.RemovalMode_REMOVAL_MODE_FREE_TIME, nil
	default:
		return pb.RemovalMode_REMOVAL_MODE_UNSPECIFIED, errors.New("invalid removal mode")
	}
}

func (in RemovalProposalInput) Proto(visitID d.VisitID) (*pb.RemovalTrigger, error) {
	if visitID == (d.VisitID{}) {
		return nil, errors.New("removal visit identifier is required")
	}
	mode, err := in.protoMode()
	if err != nil {
		return nil, err
	}
	id := visitID
	return &pb.RemovalTrigger{VisitId: append([]byte(nil), id[:]...), Mode: mode}, nil
}

func BuildRemovalRecompute(routeID d.RouteID, city string, base d.RoutePlanSnapshot, history []d.Execution, visitID d.VisitID, input RemovalProposalInput) (*pb.RecomputeRequest, error) {
	request, err := buildRecomputeBase(routeID, city, base, history)
	if err != nil {
		return nil, err
	}
	found := false
	for _, step := range base.Steps {
		if step.VisitID != visitID {
			continue
		}
		if hasExternalCommitment(step) && !input.AcknowledgeExternalCommitment {
			return nil, ErrExternalCommitmentAcknowledgementRequired
		}
		found = true
		break
	}
	if !found {
		return nil, ErrInvalidRecomputeInput
	}
	trigger, err := input.Proto(visitID)
	if err != nil {
		return nil, err
	}
	request.Trigger = &pb.RecomputeRequest_Removal{Removal: trigger}
	return request, nil
}
