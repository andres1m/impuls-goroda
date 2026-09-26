package domain

import (
	"errors"
	"strings"
	"time"
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

// ProgramBalance is reported by the user and never proves that a benefit applies.
type ProgramBalance struct {
	Program string
	Balance Money
}

func (b ProgramBalance) Validate() error {
	if strings.TrimSpace(b.Program) == "" {
		return errors.New("benefit program is required")
	}
	return b.Balance.Validate()
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
	case ParticipationNotRequired, ParticipationActionRequired, ParticipationUserReported, ParticipationProviderConfirmed, ParticipationUnavailable:
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
	ProgramBalance     *ProgramBalance
	AudienceClaims     []string
	Obligations        []Obligation
	SoftPreferences    []string
	LunchWindow        *LunchWindow
	AcceptedUnknowns   []string
}

func (c RouteConstraints) Validate() error {
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
	if strings.TrimSpace(c.LoadProfile) == "" {
		return errors.New("load profile is required")
	}
	if err := c.Budget.Validate(); err != nil {
		return err
	}
	if c.ProgramBalance != nil {
		if err := c.ProgramBalance.Validate(); err != nil {
			return err
		}
	}
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
	for _, obligation := range c.Obligations {
		if err := obligation.Validate(); err != nil {
			return err
		}
	}
	if c.LunchWindow != nil {
		return c.LunchWindow.Validate()
	}
	return nil
}
