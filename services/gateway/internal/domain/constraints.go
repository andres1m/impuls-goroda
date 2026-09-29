package domain

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

type BudgetMode string

const (
	BudgetNone     BudgetMode = "none"
	BudgetAdvisory BudgetMode = "advisory"
	BudgetStrict   BudgetMode = "strict"
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
	case BudgetAdvisory, BudgetStrict:
		if b.Limit == nil {
			return errors.New("budget limit is required")
		}
		if err := b.Limit.Validate(); err != nil {
			return err
		}
	default:
		return errors.New("invalid budget mode")
	}
	return nil
}

type EvidenceKind string

const EvidenceUserReported EvidenceKind = "user_reported"

type AudienceClaim struct {
	Audience string
	Evidence EvidenceKind
}

func (c AudienceClaim) Validate() error {
	if strings.TrimSpace(c.Audience) == "" || c.Evidence != EvidenceUserReported {
		return errors.New("invalid audience claim")
	}
	return nil
}

type RouteObligation struct {
	VisitID              *VisitID
	SessionID            *EventSessionID
	StartsAt             *time.Time
	ArrivalBufferSeconds int64
	Participation        ParticipationStatus
}

func (o RouteObligation) Validate() error {
	if o.VisitID == nil && o.SessionID == nil {
		return errors.New("obligation visit or session is required")
	}
	if o.VisitID != nil {
		if err := requiredID([16]byte(*o.VisitID)); err != nil {
			return err
		}
	}
	if o.SessionID != nil {
		if err := requiredID([16]byte(*o.SessionID)); err != nil {
			return err
		}
	}
	if o.StartsAt != nil && o.StartsAt.IsZero() {
		return errors.New("obligation start time is invalid")
	}
	if o.ArrivalBufferSeconds < 0 {
		return errors.New("arrival buffer must not be negative")
	}
	if !validParticipationStatus(o.Participation) {
		return errors.New("invalid obligation participation status")
	}
	return nil
}

type LunchWindow struct {
	Start              time.Time
	End                time.Time
	MinDurationSeconds int64
}

func (w LunchWindow) Validate() error {
	if w.Start.IsZero() || !w.End.After(w.Start) || w.MinDurationSeconds <= 0 {
		return errors.New("invalid lunch window")
	}
	if w.End.Sub(w.Start) < time.Duration(w.MinDurationSeconds)*time.Second {
		return errors.New("lunch duration exceeds its window")
	}
	return nil
}

type MovementMode string
type UnknownConditionCode string

type RouteConstraints struct {
	InterestMask       uint64
	ExcludedCategories []string
	MovementModes      []MovementMode
	LoadProfile        string
	Budget             Budget
	BenefitPrograms    []string
	PushkinCardOnly    bool
	AudienceClaims     []AudienceClaim
	Obligations        []RouteObligation
	SoftPreferences    []string
	LunchWindow        *LunchWindow
	AcceptedUnknowns   []UnknownConditionCode
	SemanticQuery      string
}

func (c *RouteConstraints) Validate() error {
	if err := c.validateMovementModes(); err != nil {
		return err
	}
	if strings.TrimSpace(c.LoadProfile) == "" {
		return errors.New("load profile is required")
	}
	if err := c.Budget.Validate(); err != nil {
		return err
	}
	if c.SemanticQuery != "" && strings.TrimSpace(c.SemanticQuery) == "" {
		return errors.New("semantic query must not be blank")
	}
	if utf8.RuneCountInString(c.SemanticQuery) > 1000 {
		return errors.New("semantic query exceeds 1000 characters")
	}
	if err := c.validateClaimsAndObligations(); err != nil {
		return err
	}
	if c.LunchWindow != nil {
		if err := c.LunchWindow.Validate(); err != nil {
			return err
		}
	}
	return c.validateStringLists()
}

func (c *RouteConstraints) validateMovementModes() error {
	if len(c.MovementModes) == 0 {
		return errors.New("at least one movement mode is required")
	}
	for _, mode := range c.MovementModes {
		if strings.TrimSpace(string(mode)) == "" {
			return errors.New("movement mode is required")
		}
	}
	return nil
}

func (c *RouteConstraints) validateClaimsAndObligations() error {
	for _, claim := range c.AudienceClaims {
		if err := claim.Validate(); err != nil {
			return err
		}
	}
	for _, obligation := range c.Obligations {
		if err := obligation.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (c *RouteConstraints) validateStringLists() error {
	if err := validateNonEmptyStrings(c.BenefitPrograms, "benefit program is required"); err != nil {
		return err
	}
	if err := validateNonEmptyStrings(c.ExcludedCategories, "excluded category is required"); err != nil {
		return err
	}
	if err := validateNonEmptyStrings(c.SoftPreferences, "soft preference is required"); err != nil {
		return err
	}
	for _, unknown := range c.AcceptedUnknowns {
		if strings.TrimSpace(string(unknown)) == "" {
			return errors.New("accepted unknown condition is required")
		}
	}
	return nil
}

func validateNonEmptyStrings(values []string, errMsg string) error {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return errors.New(errMsg)
		}
	}
	return nil
}
