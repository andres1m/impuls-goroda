package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	ProgramPushkinCard = "pushkin_card"
	// AcceptUnknownPrice in AcceptedUnknowns lets a route include visits whose price is unknown.
	AcceptUnknownPrice = "unknown_price"
)

type BudgetMode string

const (
	BudgetNone     BudgetMode = "none"
	BudgetAdvisory BudgetMode = "advisory"
	// A strict budget holds only when every price in the route has a known upper bound.
	BudgetStrict BudgetMode = "strict"
)

type Budget struct {
	Mode  BudgetMode
	Limit *Money
}

func (b Budget) Validate() error {
	switch b.Mode {
	case BudgetNone:
		if b.Limit != nil {
			return errors.New("budget without a limit must use none mode")
		}
		return nil
	case BudgetAdvisory, BudgetStrict:
		if b.Limit == nil {
			return errors.New("budget limit is required")
		}
		return b.Limit.Validate()
	default:
		return errors.New("invalid budget mode")
	}
}

type ParticipationStatus string

const (
	ParticipationNotRequired       ParticipationStatus = "not_required"
	ParticipationActionRequired    ParticipationStatus = "action_required"
	ParticipationUserReported      ParticipationStatus = "user_reported_confirmed"
	ParticipationProviderConfirmed ParticipationStatus = "provider_confirmed"
	ParticipationUnavailable       ParticipationStatus = "unavailable"
)

func (s ParticipationStatus) Validate() error {
	switch s {
	case ParticipationNotRequired,
		ParticipationActionRequired,
		ParticipationUserReported,
		ParticipationProviderConfirmed,
		ParticipationUnavailable:
		return nil
	default:
		return errors.New("invalid participation status")
	}
}

type Obligation struct {
	VisitID       *VisitID
	SessionID     *SessionID
	StartsAt      *time.Time
	ArrivalBuffer time.Duration
	Participation ParticipationStatus
}

func (o Obligation) Validate() error {
	if o.VisitID == nil && o.SessionID == nil {
		return errors.New("obligation requires a visit or a session")
	}
	if err := optionalID(o.VisitID, "visit"); err != nil {
		return err
	}
	if err := optionalID(o.SessionID, "session"); err != nil {
		return err
	}
	if !validOptionalTime(o.StartsAt) {
		return errors.New("obligation start time is invalid")
	}
	if o.ArrivalBuffer < 0 {
		return errors.New("arrival buffer must not be negative")
	}
	return o.Participation.Validate()
}

type LunchWindow struct {
	Start       time.Time
	End         time.Time
	MinDuration time.Duration
}

func (w LunchWindow) Validate() error {
	if w.Start.IsZero() || w.MinDuration <= 0 {
		return errors.New("lunch window is invalid")
	}
	if w.End.Sub(w.Start) < w.MinDuration {
		return errors.New("lunch duration exceeds its window")
	}
	return nil
}

// RouteConstraints is the confirmed user input. Load profile, programs, audiences,
// preferences and accepted unknowns are open code lists.
type RouteConstraints struct {
	InterestMask       InterestMask
	ExcludedCategories []Category
	MovementModes      []MovementMode
	LoadProfile        string
	Budget             Budget
	BenefitPrograms    []string
	AudienceClaims     []string
	Obligations        []Obligation
	SoftPreferences    []string
	LunchWindow        *LunchWindow
	AcceptedUnknowns   []string
	// Paid visits must accept the Pushkin card; free visits stay allowed.
	PushkinCardOnly bool
	// User-confirmed free-text wishes; empty plans by the interest mask alone.
	SemanticQuery string
}

// MaxSemanticQueryLength is counted in characters, not bytes.
const MaxSemanticQueryLength = 1000

func (c *RouteConstraints) Validate() error {
	for _, category := range c.ExcludedCategories {
		if err := category.Validate(); err != nil {
			return err
		}
	}
	if len(c.MovementModes) == 0 {
		return errors.New("at least one movement mode is required")
	}
	for _, mode := range c.MovementModes {
		if err := mode.Validate(); err != nil {
			return err
		}
	}
	if err := c.Budget.Validate(); err != nil {
		return err
	}
	if err := c.validateCodeLists(); err != nil {
		return err
	}
	for _, obligation := range c.Obligations {
		if err := obligation.Validate(); err != nil {
			return err
		}
	}
	if err := validateSemanticQuery(c.SemanticQuery); err != nil {
		return err
	}
	if c.LunchWindow != nil {
		return c.LunchWindow.Validate()
	}
	return nil
}

func validateSemanticQuery(query string) error {
	if query != "" && strings.TrimSpace(query) == "" {
		return errors.New("semantic query must not be blank")
	}
	if utf8.RuneCountInString(query) > MaxSemanticQueryLength {
		return fmt.Errorf("semantic query exceeds %d characters", MaxSemanticQueryLength)
	}
	return nil
}

func (c *RouteConstraints) validateCodeLists() error {
	for _, list := range []struct {
		values []string
		name   string
	}{
		{c.BenefitPrograms, "benefit program"},
		{c.AudienceClaims, "audience claim"},
		{c.SoftPreferences, "soft preference"},
		{c.AcceptedUnknowns, "accepted unknown"},
	} {
		if err := requireNonBlank(list.values, list.name); err != nil {
			return err
		}
	}
	return nil
}
