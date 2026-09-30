package domain

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

type LunchMetadata struct {
	AfterVisitID    VisitID
	DurationSeconds int64
}

func (m LunchMetadata) Validate() error {
	if err := requiredID([16]byte(m.AfterVisitID)); err != nil {
		return err
	}
	if m.DurationSeconds < 2700 || m.DurationSeconds > 3600 {
		return errors.New("lunch duration must be 45 to 60 minutes")
	}
	return nil
}

type ExternalVenueSnapshot struct {
	Provider          string
	ExternalID        string
	Title             string
	Address           string
	Position          Coordinate
	ObservedAt        time.Time
	Price             Price
	Availability      AvailabilityStatus
	HoursVerification VerificationStatus
}

func (v ExternalVenueSnapshot) Validate() error {
	if v.Provider != "2gis" || strings.TrimSpace(v.ExternalID) == "" || utf8.RuneCountInString(v.ExternalID) > 128 ||
		strings.TrimSpace(v.Title) == "" || utf8.RuneCountInString(v.Title) > 500 || utf8.RuneCountInString(v.Address) > 1000 || v.ObservedAt.IsZero() {
		return errors.New("invalid external lunch venue identity")
	}
	if err := v.Position.Validate(); err != nil {
		return err
	}
	if err := v.Price.Validate(); err != nil {
		return err
	}
	if v.Price.Status != PriceUnknown || v.Availability != AvailabilityUnknown || v.HoursVerification != VerificationUnknown {
		return errors.New("external lunch facts must remain unknown")
	}
	return nil
}
