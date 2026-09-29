package domain

import (
	"errors"
	"math"
	"strings"
	"time"
)

type Coordinate struct {
	Longitude float64 `json:"Longitude"`
	Latitude  float64 `json:"Latitude"`
}

func (c Coordinate) Validate() error {
	if !finite(c.Longitude) || !finite(c.Latitude) || c.Longitude < -180 || c.Longitude > 180 || c.Latitude < -90 ||
		c.Latitude > 90 {
		return errors.New("coordinate is outside the valid range")
	}
	return nil
}

func finite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

type DataMode string

const (
	DataLive      DataMode = "live"
	DataPrepared  DataMode = "prepared"
	DataSynthetic DataMode = "synthetic"
)

const (
	trustInvalid = iota
	trustSynthetic
	trustPrepared
	trustLive
)

func (m DataMode) trust() int {
	switch m {
	case DataLive:
		return trustLive
	case DataPrepared:
		return trustPrepared
	case DataSynthetic:
		return trustSynthetic
	default:
		return trustInvalid
	}
}

func (m DataMode) Validate() error {
	if m.trust() == 0 {
		return errors.New("invalid data mode")
	}
	return nil
}

// Weakest keeps a result from claiming more trust than its least trusted fact.
func Weakest(a, b DataMode) DataMode {
	if a.trust() <= b.trust() {
		return a
	}
	return b
}

type Provenance struct {
	SourceName      string          `json:"SourceName"`
	SourceURL       *string         `json:"SourceURL"`
	SourceRecordID  *SourceRecordID `json:"SourceRecordID"`
	SourceUpdatedAt *time.Time      `json:"SourceUpdatedAt"`
	FetchedAt       time.Time       `json:"FetchedAt"`
	VerifiedAt      *time.Time      `json:"VerifiedAt"`
}

func (p Provenance) Validate() error {
	if strings.TrimSpace(p.SourceName) == "" || p.FetchedAt.IsZero() {
		return errors.New("provenance source and fetch time are required")
	}
	if p.SourceURL != nil && strings.TrimSpace(*p.SourceURL) == "" {
		return errors.New("provenance source url must not be empty")
	}
	if err := optionalID(p.SourceRecordID, "source record"); err != nil {
		return err
	}
	if !validOptionalTime(p.SourceUpdatedAt) || !validOptionalTime(p.VerifiedAt) {
		return errors.New("provenance time is invalid")
	}
	return nil
}

func validOptionalTime(t *time.Time) bool {
	return t == nil || !t.IsZero()
}

type Availability string

const (
	AvailabilityAvailable            Availability = "available"
	AvailabilityRegistrationRequired Availability = "registration_required"
	AvailabilitySoldOut              Availability = "sold_out"
	AvailabilityCancelled            Availability = "cancelled"
	AvailabilityUnknown              Availability = "unknown"
)

func (a Availability) Validate() error {
	switch a {
	case AvailabilityAvailable,
		AvailabilityRegistrationRequired,
		AvailabilitySoldOut,
		AvailabilityCancelled,
		AvailabilityUnknown:
		return nil
	default:
		return errors.New("invalid availability")
	}
}

type VerificationStatus string

const (
	VerificationVerified    VerificationStatus = "verified"
	VerificationEstimated   VerificationStatus = "estimated"
	VerificationUnknown     VerificationStatus = "unknown"
	VerificationUnavailable VerificationStatus = "unavailable"
)

func (s VerificationStatus) Validate() error {
	switch s {
	case VerificationVerified, VerificationEstimated, VerificationUnknown, VerificationUnavailable:
		return nil
	default:
		return errors.New("invalid verification status")
	}
}

type MovementMode string

const (
	MovementWalk    MovementMode = "walk"
	MovementTransit MovementMode = "transit"
	MovementCar     MovementMode = "car"
)

func (m MovementMode) Validate() error {
	switch m {
	case MovementWalk, MovementTransit, MovementCar:
		return nil
	default:
		return errors.New("invalid movement mode")
	}
}
