package command

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
)

type Operation string

const (
	OptimizeRoutes           Operation = "optimizeRoutes"
	SaveRoute                Operation = "saveRoute"
	ProposePanicReroute      Operation = "proposePanicReroute"
	ApplyRouteProposal       Operation = "applyRouteProposal"
	RejectRouteProposal      Operation = "rejectRouteProposal"
	SetVisitPin              Operation = "setVisitPin"
	ProposeVisitRemoval      Operation = "proposeVisitRemoval"
	UpdateVisitParticipation Operation = "updateVisitParticipation"
	UpdateVisitExecution     Operation = "updateVisitExecution"
	CreateRouteShare         Operation = "createRouteShare"
	RevokeRouteShare         Operation = "revokeRouteShare"
	CopySharedRoute          Operation = "copySharedRoute"
	DeleteRoute              Operation = "deleteRoute"
)

var ErrIdempotencyKeyReused = errors.New("idempotency key was reused")

type RevisionConflict struct {
	Current domain.RouteRevisionNumber
}

func (e *RevisionConflict) Error() string {
	return fmt.Sprintf("route revision is stale: current revision %d", e.Current)
}

type Envelope struct {
	ActorID     domain.UserID
	Operation   Operation
	Key         [16]byte
	RequestHash [32]byte
}

func (e Envelope) Validate() error {
	if e.ActorID == (domain.UserID{}) || e.Key == ([16]byte{}) || e.RequestHash == ([32]byte{}) {
		return errors.New("actor, key, and request hash are required")
	}
	if !e.Operation.Valid() {
		return errors.New("unsupported command operation")
	}
	return nil
}

func (o Operation) Valid() bool {
	switch o {
	case OptimizeRoutes, SaveRoute, ProposePanicReroute, ApplyRouteProposal,
		RejectRouteProposal, SetVisitPin, ProposeVisitRemoval, UpdateVisitParticipation,
		UpdateVisitExecution, CreateRouteShare, RevokeRouteShare, CopySharedRoute, DeleteRoute:
		return true
	default:
		return false
	}
}

type Result struct {
	HTTPStatus        int
	ResponseBody      json.RawMessage
	RouteID           *domain.RouteID
	ResultingRevision *domain.RouteRevisionNumber
	CreatedAt         time.Time
	Replayed          bool
}

func (r Result) Validate() error {
	if r.HTTPStatus < http.StatusOK || r.HTTPStatus >= http.StatusMultipleChoices {
		return errors.New("command result must have a successful HTTP status")
	}
	if r.RouteID == nil && r.ResultingRevision != nil {
		return errors.New("resulting revision requires a route")
	}
	if r.RouteID != nil && *r.RouteID == (domain.RouteID{}) {
		return errors.New("result route identifier is empty")
	}
	if r.ResultingRevision != nil {
		if err := r.ResultingRevision.Validate(); err != nil {
			return err
		}
	}
	if len(r.ResponseBody) == 0 {
		return errors.New("response template is required")
	}
	var body map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(r.ResponseBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || body == nil {
		return errors.New("response template must be a JSON object")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("response template must contain one JSON object")
	}
	if err := rejectPrivateFields(r.ResponseBody); err != nil {
		return err
	}
	return nil
}

func rejectPrivateFields(raw json.RawMessage) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	var visit func(any) error
	visit = func(value any) error {
		switch item := value.(type) {
		case map[string]any:
			for key, child := range item {
				switch key {
				case "request_id", "share_token", "access_token", "init_data", "private_reference":
					return fmt.Errorf("response template contains private field %q", key)
				}
				if err := visit(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range item {
				if err := visit(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return visit(value)
}
