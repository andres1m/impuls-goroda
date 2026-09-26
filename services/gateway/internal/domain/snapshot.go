package domain

import (
	"errors"
	"math"
	"strings"
	"time"
)

type AvailabilityStatus string

const (
	AvailabilityAvailable            AvailabilityStatus = "available"
	AvailabilityRegistrationRequired AvailabilityStatus = "registration_required"
	AvailabilitySoldOut              AvailabilityStatus = "sold_out"
	AvailabilityCancelled            AvailabilityStatus = "cancelled"
	AvailabilityUnknown              AvailabilityStatus = "unknown"
)

type CatalogSnapshot struct {
	PlaceID             *PlaceID
	EventID             *EventID
	SessionID           *EventSessionID
	Title               string
	Category            string
	InterestMask        uint64
	Availability        AvailabilityStatus
	RegistrationDetails string
	AgeRequirements     string
	SessionStartsAt     *time.Time
	SessionEndsAt       *time.Time
	SessionVersion      string
	DataMode            DataMode
	Provenance          FactProvenance
}

func (s CatalogSnapshot) Validate() error {
	if strings.TrimSpace(s.Title) == "" {
		return errors.New("catalog snapshot title is required")
	}
	if s.PlaceID != nil {
		if err := requiredID([16]byte(*s.PlaceID)); err != nil {
			return err
		}
	}
	if s.EventID != nil {
		if s.PlaceID == nil {
			return errors.New("catalog event requires a place")
		}
		if err := requiredID([16]byte(*s.EventID)); err != nil {
			return err
		}
	}
	if s.SessionID != nil {
		if s.EventID == nil {
			return errors.New("catalog session requires an event")
		}
		if err := requiredID([16]byte(*s.SessionID)); err != nil {
			return err
		}
	}
	if (s.SessionStartsAt == nil) != (s.SessionEndsAt == nil) {
		return errors.New("catalog session times must be provided together")
	}
	if s.SessionStartsAt != nil && !s.SessionEndsAt.After(*s.SessionStartsAt) {
		return errors.New("catalog session interval is invalid")
	}
	switch s.Availability {
	case AvailabilityAvailable, AvailabilityRegistrationRequired, AvailabilitySoldOut, AvailabilityCancelled, AvailabilityUnknown:
	default:
		return errors.New("invalid catalog availability")
	}
	if err := s.DataMode.Validate(); err != nil {
		return err
	}
	return s.Provenance.Validate()
}

type ParticipationSnapshot struct {
	Status   ParticipationStatus
	Evidence EvidenceSource
}

func (s ParticipationSnapshot) Validate() error {
	if !validParticipationStatus(s.Status) {
		return errors.New("invalid participation snapshot status")
	}
	switch s.Evidence {
	case EvidenceNone, EvidenceUser, EvidenceProvider:
	default:
		return errors.New("invalid participation snapshot evidence")
	}
	if s.Status == ParticipationProviderConfirmed && s.Evidence != EvidenceProvider {
		return errors.New("provider confirmation requires provider evidence")
	}
	return nil
}

type ConstraintStrength string

const (
	ConstraintHard ConstraintStrength = "hard"
	ConstraintSoft ConstraintStrength = "soft"
)

type ConstraintOutcome string

const (
	ConstraintSatisfied   ConstraintOutcome = "satisfied"
	ConstraintConditional ConstraintOutcome = "conditional"
)

type AppliedConstraint struct {
	Code     string
	Strength ConstraintStrength
	Outcome  ConstraintOutcome
	Message  string
}

func (c AppliedConstraint) Validate() error {
	if strings.TrimSpace(c.Code) == "" || strings.TrimSpace(c.Message) == "" {
		return errors.New("applied constraint code and message are required")
	}
	if c.Strength != ConstraintHard && c.Strength != ConstraintSoft {
		return errors.New("invalid constraint strength")
	}
	if c.Outcome != ConstraintSatisfied && c.Outcome != ConstraintConditional {
		return errors.New("invalid constraint outcome")
	}
	return nil
}

type RouteStep struct {
	VisitID            VisitID
	Kind               VisitKind
	Position           int
	ArrivalAt          time.Time
	VisitStartAt       time.Time
	VisitEndAt         time.Time
	DepartureAt        time.Time
	MinDurationSeconds int64
	Pinned             bool
	Obligation         bool
	Participation      ParticipationSnapshot
	Catalog            *CatalogSnapshot
	Cost               *CostSnapshot
	AppliedConstraints []AppliedConstraint
}

func (s RouteStep) Validate() error {
	if err := requiredID([16]byte(s.VisitID)); err != nil {
		return err
	}
	if s.Position <= 0 || s.MinDurationSeconds < 0 {
		return errors.New("route step position or duration is invalid")
	}
	switch s.Kind {
	case VisitFreeTime:
		if s.Catalog != nil || s.Cost != nil {
			return errors.New("free time step must not have catalog or cost snapshot")
		}
	case VisitPlace:
		if s.MinDurationSeconds == 0 || s.Catalog == nil || s.Cost == nil {
			return errors.New("visit step requires duration, catalog and cost snapshots")
		}
	default:
		return errors.New("invalid route step kind")
	}
	if s.ArrivalAt.IsZero() || s.VisitStartAt.Before(s.ArrivalAt) || !s.VisitEndAt.After(s.VisitStartAt) || s.DepartureAt.Before(s.VisitEndAt) {
		return errors.New("route step interval is invalid")
	}
	if s.VisitEndAt.Sub(s.VisitStartAt) < time.Duration(s.MinDurationSeconds)*time.Second {
		return errors.New("route step is shorter than its minimum duration")
	}
	if err := s.Participation.Validate(); err != nil {
		return err
	}
	if s.Catalog != nil {
		if err := s.Catalog.Validate(); err != nil {
			return err
		}
	}
	if s.Cost != nil {
		if err := s.Cost.Validate(); err != nil {
			return err
		}
	}
	for _, constraint := range s.AppliedConstraints {
		if err := constraint.Validate(); err != nil {
			return err
		}
	}
	return nil
}

type LegEndpointKind string

const (
	LegOrigin      LegEndpointKind = "origin"
	LegVisit       LegEndpointKind = "visit"
	LegDestination LegEndpointKind = "destination"
)

type VerificationStatus string

const (
	VerificationVerified    VerificationStatus = "verified"
	VerificationEstimated   VerificationStatus = "estimated"
	VerificationUnknown     VerificationStatus = "unknown"
	VerificationUnavailable VerificationStatus = "unavailable"
)

type LegEvidence struct {
	Provider    string
	Method      string
	ObservedAt  time.Time
	Mode        string
	Limitations []string
}

func (e LegEvidence) Validate() error {
	if strings.TrimSpace(e.Provider) == "" || strings.TrimSpace(e.Method) == "" || e.ObservedAt.IsZero() || strings.TrimSpace(e.Mode) == "" {
		return errors.New("leg evidence is incomplete")
	}
	return nil
}

type RouteLeg struct {
	Position       int
	FromKind       LegEndpointKind
	ToKind         LegEndpointKind
	FromVisitID    *VisitID
	ToVisitID      *VisitID
	DepartureAt    time.Time
	ArrivalAt      time.Time
	Mode           MovementMode
	DistanceMeters *float64
	Geometry       []Coordinate
	Verification   VerificationStatus
	Evidence       LegEvidence
	Cost           CostSnapshot
}

func (l RouteLeg) Validate() error {
	if l.Position <= 0 || l.DepartureAt.IsZero() || l.ArrivalAt.Before(l.DepartureAt) || strings.TrimSpace(string(l.Mode)) == "" {
		return errors.New("route leg position, time or mode is invalid")
	}
	if l.FromKind == LegOrigin {
		if l.FromVisitID != nil {
			return errors.New("origin leg must not have a source visit")
		}
	} else if l.FromKind == LegVisit {
		if l.FromVisitID == nil {
			return errors.New("visit leg requires a source visit")
		}
	} else {
		return errors.New("invalid leg source kind")
	}
	if l.ToKind == LegDestination {
		if l.ToVisitID != nil {
			return errors.New("destination leg must not have a target visit")
		}
	} else if l.ToKind == LegVisit {
		if l.ToVisitID == nil {
			return errors.New("visit leg requires a target visit")
		}
	} else {
		return errors.New("invalid leg target kind")
	}
	for _, visitID := range []*VisitID{l.FromVisitID, l.ToVisitID} {
		if visitID != nil {
			if err := requiredID([16]byte(*visitID)); err != nil {
				return err
			}
		}
	}
	if l.DistanceMeters != nil && (math.IsNaN(*l.DistanceMeters) || math.IsInf(*l.DistanceMeters, 0) || *l.DistanceMeters < 0) {
		return errors.New("route leg distance must not be negative")
	}
	for _, point := range l.Geometry {
		if err := point.Validate(); err != nil {
			return err
		}
	}
	switch l.Verification {
	case VerificationVerified, VerificationEstimated, VerificationUnknown, VerificationUnavailable:
	default:
		return errors.New("invalid leg verification status")
	}
	if err := l.Evidence.Validate(); err != nil {
		return err
	}
	return l.Cost.Validate()
}
