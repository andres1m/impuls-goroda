package domain

import (
	"errors"
	"strings"
	"time"
)

type ChangeKind string

const (
	ChangeKept                ChangeKind = "kept"
	ChangeRemoved             ChangeKind = "removed"
	ChangeReplaced            ChangeKind = "replaced"
	ChangeTimeShifted         ChangeKind = "time_shifted"
	ChangeCostChanged         ChangeKind = "cost_changed"
	ChangeParticipationAction ChangeKind = "participation_action"
	ChangeVerificationChanged ChangeKind = "verification_changed"
)

type ChangeDetails interface {
	isChangeDetails()
}

type TimeShift struct {
	Delta time.Duration
}

type CostChange struct {
	Before Money
	After  Money
}

type ParticipationAction struct {
	Action string
}

type VerificationChange struct {
	Before VerificationStatus
	After  VerificationStatus
}

func (TimeShift) isChangeDetails()           {}
func (CostChange) isChangeDetails()          {}
func (ParticipationAction) isChangeDetails() {}
func (VerificationChange) isChangeDetails()  {}

type RouteChange struct {
	Kind          ChangeKind
	Scope         Scope
	BeforeVisitID *VisitID
	AfterVisitID  *VisitID
	LegPosition   *int
	Message       string
	Details       ChangeDetails
}

func (c RouteChange) Validate() error {
	if strings.TrimSpace(c.Message) == "" {
		return errors.New("route change message is required")
	}
	if err := c.validateScope(); err != nil {
		return err
	}
	switch c.Kind {
	case ChangeKept, ChangeRemoved, ChangeReplaced, ChangeTimeShifted, ChangeParticipationAction:
		if c.Scope != ScopeVisit {
			return errors.New("visit change requires visit scope")
		}
	case ChangeVerificationChanged:
		if c.Scope != ScopeLeg {
			return errors.New("verification change requires leg scope")
		}
	case ChangeCostChanged:
	default:
		return errors.New("invalid route change kind")
	}
	if err := c.validateVisits(); err != nil {
		return err
	}
	return c.validateDetails()
}

func (c RouteChange) validateScope() error {
	if err := optionalID(c.BeforeVisitID, "visit"); err != nil {
		return err
	}
	if err := optionalID(c.AfterVisitID, "visit"); err != nil {
		return err
	}
	hasVisit := c.BeforeVisitID != nil || c.AfterVisitID != nil
	switch c.Scope {
	case ScopeRoute:
		if hasVisit || c.LegPosition != nil {
			return errors.New("route change must not target a visit or leg")
		}
	case ScopeVisit:
		if !hasVisit || c.LegPosition != nil {
			return errors.New("visit change requires only visits")
		}
	case ScopeLeg:
		if hasVisit || c.LegPosition == nil || *c.LegPosition < 1 {
			return errors.New("leg change requires only a positive leg position")
		}
	default:
		return errors.New("invalid route change scope")
	}
	return nil
}

func (c RouteChange) validateVisits() error {
	switch c.Kind {
	case ChangeRemoved:
		if c.AfterVisitID != nil {
			return errors.New("removed change must not have a next visit")
		}
	case ChangeReplaced:
		if c.BeforeVisitID == nil || c.AfterVisitID == nil {
			return errors.New("replaced change requires previous and next visits")
		}
	case ChangeKept:
		if c.BeforeVisitID == nil {
			return errors.New("kept change requires the previous visit")
		}
	}
	return nil
}

func (c RouteChange) validateDetails() error {
	switch c.Kind {
	case ChangeTimeShifted:
		if _, ok := c.Details.(TimeShift); !ok {
			return errors.New("time shift change requires a delta")
		}
	case ChangeCostChanged:
		d, ok := c.Details.(CostChange)
		if !ok {
			return errors.New("cost change requires before and after amounts")
		}
		if err := d.Before.Validate(); err != nil {
			return err
		}
		if err := d.After.Validate(); err != nil {
			return err
		}
		if d.Before.Currency != d.After.Currency {
			return errors.New("cost change currencies differ")
		}
	case ChangeParticipationAction:
		d, ok := c.Details.(ParticipationAction)
		if !ok || strings.TrimSpace(d.Action) == "" {
			return errors.New("participation change requires an action")
		}
	case ChangeVerificationChanged:
		d, ok := c.Details.(VerificationChange)
		if !ok {
			return errors.New("verification change requires before and after states")
		}
		if err := d.Before.Validate(); err != nil {
			return err
		}
		return d.After.Validate()
	default:
		if c.Details != nil {
			return errors.New("route change kind does not carry details")
		}
	}
	return nil
}
