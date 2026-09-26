package domain

import (
	"errors"
	"time"
)

type VisitKind string

const (
	VisitPlace    VisitKind = "visit"
	VisitFreeTime VisitKind = "free_time"
)

type RouteVisit struct {
	RouteID           RouteID
	ID                VisitID
	Kind              VisitKind
	City              string
	PlaceID           *PlaceID
	EntranceID        *EntranceID
	EventID           *EventID
	SessionID         *EventSessionID
	PriceOfferID      *PriceOfferID
	CreatedInRevision RouteRevisionNumber
	CreatedAt         time.Time
}

func (v RouteVisit) Validate() error {
	if err := requiredID([16]byte(v.RouteID)); err != nil {
		return err
	}
	if err := requiredID([16]byte(v.ID)); err != nil {
		return err
	}
	if v.City == "" || v.CreatedAt.IsZero() {
		return errors.New("visit city and creation time are required")
	}
	if err := v.CreatedInRevision.Validate(); err != nil {
		return err
	}
	switch v.Kind {
	case VisitFreeTime:
		if v.PlaceID != nil || v.EntranceID != nil || v.EventID != nil || v.SessionID != nil || v.PriceOfferID != nil {
			return errors.New("free time cannot reference catalog entities")
		}
	case VisitPlace:
		if v.PlaceID == nil {
			return errors.New("place visit requires a place")
		}
	default:
		return errors.New("invalid visit kind")
	}
	if v.EntranceID != nil && v.PlaceID == nil || v.EventID != nil && v.PlaceID == nil ||
		v.SessionID != nil && v.EventID == nil || v.PriceOfferID != nil && v.SessionID == nil {
		return errors.New("incomplete catalog reference chain")
	}
	if v.PlaceID != nil && *v.PlaceID == (PlaceID{}) || v.EntranceID != nil && *v.EntranceID == (EntranceID{}) ||
		v.EventID != nil && *v.EventID == (EventID{}) || v.SessionID != nil && *v.SessionID == (EventSessionID{}) ||
		v.PriceOfferID != nil && *v.PriceOfferID == (PriceOfferID{}) {
		return errors.New("catalog reference cannot be empty")
	}
	return nil
}
