package lifecycle

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"
	"unicode"
	"unicode/utf8"

	pb "github.com/andres1m/impuls-goroda/proto/gateway/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

var ErrInvalidChange = errors.New("invalid catalog lifecycle change")

type Change struct {
	SchemaVersion   uint32
	DeliveryID      uuid.UUID
	ChangeID        uuid.UUID
	City            string
	CatalogRevision int64
	EventID         *uuid.UUID
	SessionID       *uuid.UUID
	OldStatus       string
	NewStatus       string
	SourceRecordID  string
	DataMode        string
	ObservedAt      time.Time
	Reason          string
}

func Decode(request *pb.DeliverCatalogLifecycleRequest) (Change, [32]byte, error) {
	if request == nil || request.SchemaVersion != 1 || proto.Size(request) > 64*1024 || len(request.ProtoReflect().GetUnknown()) != 0 || request.CatalogRevision <= 0 || request.ObservedAt == nil || request.ObservedAt.CheckValid() != nil || !text(request.City, 64, true) || !text(request.SourceRecordId, 512, false) || !text(request.Reason, 512, false) || !availability(request.NewAvailabilityStatus) || request.OldAvailabilityStatus != "" && !availability(request.OldAvailabilityStatus) {
		return Change{}, [32]byte{}, ErrInvalidChange
	}
	if request.DataMode != "live" && request.DataMode != "prepared" && request.DataMode != "synthetic" {
		return Change{}, [32]byte{}, ErrInvalidChange
	}
	delivery, err := uuid.FromBytes(request.DeliveryId)
	if err != nil || delivery == uuid.Nil {
		return Change{}, [32]byte{}, ErrInvalidChange
	}
	changeID, err := uuid.FromBytes(request.ChangeId)
	if err != nil || changeID == uuid.Nil {
		return Change{}, [32]byte{}, ErrInvalidChange
	}
	event, err := optionalID(request.EventId)
	if err != nil {
		return Change{}, [32]byte{}, err
	}
	session, err := optionalID(request.SessionId)
	if err != nil || event == nil && session == nil {
		return Change{}, [32]byte{}, ErrInvalidChange
	}
	change := Change{SchemaVersion: request.SchemaVersion, DeliveryID: delivery, ChangeID: changeID, City: request.City, CatalogRevision: request.CatalogRevision, EventID: event, SessionID: session, OldStatus: request.OldAvailabilityStatus, NewStatus: request.NewAvailabilityStatus, SourceRecordID: request.SourceRecordId, DataMode: request.DataMode, ObservedAt: request.ObservedAt.AsTime(), Reason: request.Reason}
	encoded, err := json.Marshal(change)
	if err != nil {
		return Change{}, [32]byte{}, ErrInvalidChange
	}
	return change, sha256.Sum256(encoded), nil
}

func optionalID(raw []byte) (*uuid.UUID, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	id, err := uuid.FromBytes(raw)
	if err != nil || id == uuid.Nil {
		return nil, ErrInvalidChange
	}
	return &id, nil
}

func text(value string, limit int, required bool) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > limit || required && value == "" {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func availability(value string) bool {
	return value == "available" || value == "registration_required" || value == "sold_out" || value == "cancelled" || value == "unknown"
}
