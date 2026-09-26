package domain

import (
	"errors"
	"strings"
	"time"
)

type Place struct {
	ID           PlaceID
	City         string
	Title        string
	Category     *Category
	InterestMask InterestMask
	Location     Coordinate
	DataMode     DataMode
	Provenance   Provenance
}

func (p Place) Validate() error {
	if err := requireID(p.ID, "place"); err != nil {
		return err
	}
	if strings.TrimSpace(p.City) == "" || strings.TrimSpace(p.Title) == "" {
		return errors.New("place city and title are required")
	}
	if p.Category != nil {
		if err := p.Category.Validate(); err != nil {
			return err
		}
	}
	if err := p.Location.Validate(); err != nil {
		return err
	}
	if err := p.DataMode.Validate(); err != nil {
		return err
	}
	return p.Provenance.Validate()
}

type Accessibility string

const (
	AccessibilityConfirmed   Accessibility = "confirmed"
	AccessibilityUnavailable Accessibility = "unavailable"
	AccessibilityUnknown     Accessibility = "unknown"
)

type Entrance struct {
	ID            EntranceID
	PlaceID       PlaceID
	Location      Coordinate
	AllowedModes  []MovementMode
	Accessibility Accessibility
	Verification  VerificationStatus
}

func (e Entrance) Validate() error {
	if err := requireID(e.ID, "entrance"); err != nil {
		return err
	}
	if err := requireID(e.PlaceID, "place"); err != nil {
		return err
	}
	if err := e.Location.Validate(); err != nil {
		return err
	}
	for _, mode := range e.AllowedModes {
		if err := mode.Validate(); err != nil {
			return err
		}
	}
	switch e.Accessibility {
	case AccessibilityConfirmed, AccessibilityUnavailable, AccessibilityUnknown:
	default:
		return errors.New("invalid entrance accessibility")
	}
	return e.Verification.Validate()
}

type Event struct {
	ID           EventID
	PlaceID      PlaceID
	Title        string
	Category     Category
	InterestMask InterestMask
	AgeMin       *int
	AgeMax       *int
	DataMode     DataMode
	Provenance   Provenance
}

func (e Event) Validate() error {
	if err := requireID(e.ID, "event"); err != nil {
		return err
	}
	if err := requireID(e.PlaceID, "place"); err != nil {
		return err
	}
	if strings.TrimSpace(e.Title) == "" {
		return errors.New("event title is required")
	}
	if err := e.Category.Validate(); err != nil {
		return err
	}
	if err := validateAgeRange(e.AgeMin, e.AgeMax); err != nil {
		return err
	}
	if err := e.DataMode.Validate(); err != nil {
		return err
	}
	return e.Provenance.Validate()
}

func validateAgeRange(lower, upper *int) error {
	if lower != nil && *lower < 0 || upper != nil && *upper < 0 {
		return errors.New("age bound must not be negative")
	}
	if lower != nil && upper != nil && *lower > *upper {
		return errors.New("age range is inverted")
	}
	return nil
}

type AccessType string

const (
	AccessFree         AccessType = "free"
	AccessRegistration AccessType = "registration"
	AccessTicket       AccessType = "ticket"
)

type Session struct {
	ID                     SessionID
	EventID                EventID
	Window                 VisitWindow
	RegistrationDeadline   *time.Time
	Access                 AccessType
	Availability           Availability
	AvailabilityObservedAt *time.Time
	IsHard                 bool
	Version                int64
	DataMode               DataMode
	Provenance             Provenance
}

func (s Session) Validate() error {
	if err := requireID(s.ID, "session"); err != nil {
		return err
	}
	if err := requireID(s.EventID, "event"); err != nil {
		return err
	}
	if err := s.Window.Validate(); err != nil {
		return err
	}
	if !validOptionalTime(s.RegistrationDeadline) || !validOptionalTime(s.AvailabilityObservedAt) {
		return errors.New("session time is invalid")
	}
	switch s.Access {
	case AccessFree, AccessRegistration, AccessTicket:
	default:
		return errors.New("invalid session access type")
	}
	if err := s.Availability.Validate(); err != nil {
		return err
	}
	if s.Version <= 0 {
		return errors.New("session version must be positive")
	}
	if err := s.DataMode.Validate(); err != nil {
		return err
	}
	return s.Provenance.Validate()
}

type Audience string

const (
	AudienceGeneral Audience = "general"
	AudienceChild   Audience = "child"
	AudienceStudent Audience = "student"
	AudienceSenior  Audience = "senior"
	AudienceOther   Audience = "other"
)

func (a Audience) Validate() error {
	switch a {
	case AudienceGeneral, AudienceChild, AudienceStudent, AudienceSenior, AudienceOther:
		return nil
	default:
		return errors.New("invalid audience")
	}
}

type PriceOffer struct {
	ID                PriceOfferID
	SessionID         SessionID
	Price             Price
	Audience          Audience
	EligibilityAgeMin *int
	EligibilityAgeMax *int
	// Programs that may pay for the ticket; eligibility never implies a zero price.
	BenefitPrograms []string
	ValidUntil      *time.Time
	Provenance      Provenance
}

func (o PriceOffer) Validate() error {
	if err := requireID(o.ID, "price offer"); err != nil {
		return err
	}
	if err := requireID(o.SessionID, "session"); err != nil {
		return err
	}
	if err := o.Price.Validate(); err != nil {
		return err
	}
	if err := o.Audience.Validate(); err != nil {
		return err
	}
	if err := validateAgeRange(o.EligibilityAgeMin, o.EligibilityAgeMax); err != nil {
		return err
	}
	if err := requireNonBlank(o.BenefitPrograms, "benefit program"); err != nil {
		return err
	}
	if !validOptionalTime(o.ValidUntil) {
		return errors.New("price offer validity is invalid")
	}
	return o.Provenance.Validate()
}

func requireNonBlank(values []string, name string) error {
	for _, v := range values {
		if strings.TrimSpace(v) == "" {
			return errors.New(name + " must not be empty")
		}
	}
	return nil
}
