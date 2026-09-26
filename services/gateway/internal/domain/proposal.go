package domain

import (
	"errors"
	"strings"
	"time"
)

type ProposalReason string

const (
	ProposalDelay   ProposalReason = "delay"
	ProposalCancel  ProposalReason = "cancel"
	ProposalDelete  ProposalReason = "delete"
	ProposalPin     ProposalReason = "pin"
	ProposalWeather ProposalReason = "weather"
)

type ProposalState string

const (
	ProposalPending     ProposalState = "pending"
	ProposalApplied     ProposalState = "applied"
	ProposalRejected    ProposalState = "rejected"
	ProposalInvalidated ProposalState = "invalidated"
)

type RouteChangeKind string

type RouteChangeScope string

const (
	ChangeRouteScope RouteChangeScope = "route"
	ChangeVisitScope RouteChangeScope = "visit"
	ChangeLegScope   RouteChangeScope = "leg"
)

const (
	ChangeKept                RouteChangeKind = "kept"
	ChangeRemoved             RouteChangeKind = "removed"
	ChangeReplaced            RouteChangeKind = "replaced"
	ChangeTimeShifted         RouteChangeKind = "time_shifted"
	ChangeCostChanged         RouteChangeKind = "cost_changed"
	ChangeParticipationAction RouteChangeKind = "participation_action"
	ChangeVerificationChanged RouteChangeKind = "verification_changed"
)

type RouteChange struct {
	Kind               RouteChangeKind
	Scope              RouteChangeScope
	BeforeVisitID      *VisitID
	AfterVisitID       *VisitID
	LegPosition        *int
	TimeShiftSeconds   *int64
	BeforeCost         *Money
	AfterCost          *Money
	Action             string
	BeforeVerification *VerificationStatus
	AfterVerification  *VerificationStatus
	Message            string
}

func (c RouteChange) Validate() error {
	switch c.Kind {
	case ChangeKept, ChangeRemoved, ChangeReplaced, ChangeTimeShifted, ChangeCostChanged, ChangeParticipationAction, ChangeVerificationChanged:
	default:
		return errors.New("invalid route change kind")
	}
	for _, visitID := range []*VisitID{c.BeforeVisitID, c.AfterVisitID} {
		if visitID != nil {
			if err := requiredID([16]byte(*visitID)); err != nil {
				return err
			}
		}
	}
	switch c.Scope {
	case ChangeRouteScope:
		if c.BeforeVisitID != nil || c.AfterVisitID != nil || c.LegPosition != nil {
			return errors.New("route change must not identify a visit or leg")
		}
	case ChangeVisitScope:
		if c.LegPosition != nil || (c.BeforeVisitID == nil && c.AfterVisitID == nil) {
			return errors.New("visit change requires a visit")
		}
	case ChangeLegScope:
		if c.LegPosition == nil || *c.LegPosition <= 0 || c.BeforeVisitID != nil || c.AfterVisitID != nil {
			return errors.New("leg change requires a positive leg position")
		}
	default:
		return errors.New("invalid route change scope")
	}
	if c.Kind == ChangeRemoved && (c.BeforeVisitID == nil || c.AfterVisitID != nil) {
		return errors.New("removed change requires only the previous visit")
	}
	if c.Kind == ChangeReplaced && (c.BeforeVisitID == nil || c.AfterVisitID == nil) {
		return errors.New("replaced change requires previous and next visits")
	}
	if (c.Kind == ChangeKept || c.Kind == ChangeRemoved || c.Kind == ChangeReplaced || c.Kind == ChangeTimeShifted || c.Kind == ChangeParticipationAction) && c.Scope != ChangeVisitScope {
		return errors.New("visit change kind requires visit scope")
	}
	if c.Kind == ChangeVerificationChanged && c.Scope != ChangeLegScope {
		return errors.New("verification change requires leg scope")
	}
	if c.Kind == ChangeTimeShifted && c.TimeShiftSeconds == nil {
		return errors.New("time shift change requires a delta")
	}
	if c.Kind == ChangeCostChanged {
		if c.BeforeCost == nil || c.AfterCost == nil {
			return errors.New("cost change requires before and after amounts")
		}
		if err := c.BeforeCost.Validate(); err != nil {
			return err
		}
		if err := c.AfterCost.Validate(); err != nil {
			return err
		}
		if c.BeforeCost.Currency != c.AfterCost.Currency {
			return errors.New("cost change currencies must match")
		}
	}
	if c.Kind == ChangeParticipationAction && strings.TrimSpace(c.Action) == "" {
		return errors.New("participation change requires an action")
	}
	if c.Kind == ChangeVerificationChanged {
		if c.BeforeVerification == nil || c.AfterVerification == nil || !validVerification(*c.BeforeVerification) || !validVerification(*c.AfterVerification) {
			return errors.New("verification change requires valid before and after states")
		}
	}
	if strings.TrimSpace(c.Message) == "" {
		return errors.New("route change message is required")
	}
	return nil
}

func validVerification(status VerificationStatus) bool {
	switch status {
	case VerificationVerified, VerificationEstimated, VerificationUnknown, VerificationUnavailable:
		return true
	default:
		return false
	}
}

type RouteProposal struct {
	ID                  ProposalID
	RouteID             RouteID
	BaseRevision        RouteRevisionNumber
	BaseCatalogRevision CatalogRevision
	Reason              ProposalReason
	State               ProposalState
	Candidate           RoutePlanSnapshot
	Changes             []RouteChange
	Conflicts           []Conflict
	EffectiveStartAt    *time.Time
	CreatedAt           time.Time
	ResolvedAt          *time.Time
	AppliedRevision     *RouteRevisionNumber
}

func (p RouteProposal) Validate() error {
	if err := requiredID([16]byte(p.ID)); err != nil {
		return err
	}
	if err := requiredID([16]byte(p.RouteID)); err != nil {
		return err
	}
	if err := p.BaseRevision.Validate(); err != nil {
		return err
	}
	if err := p.BaseCatalogRevision.Validate(); err != nil {
		return err
	}
	switch p.Reason {
	case ProposalDelay, ProposalCancel, ProposalDelete, ProposalPin, ProposalWeather:
	default:
		return errors.New("invalid proposal reason")
	}
	if err := p.Candidate.Validate(); err != nil {
		return err
	}
	for _, change := range p.Changes {
		if err := change.Validate(); err != nil {
			return err
		}
	}
	for _, conflict := range p.Conflicts {
		if err := conflict.Validate(); err != nil {
			return err
		}
	}
	if p.EffectiveStartAt != nil && p.EffectiveStartAt.IsZero() {
		return errors.New("proposal effective start is invalid")
	}
	if p.CreatedAt.IsZero() {
		return errors.New("proposal creation time is required")
	}
	switch p.State {
	case ProposalPending:
		if p.ResolvedAt != nil || p.AppliedRevision != nil {
			return errors.New("pending proposal must not be resolved")
		}
	case ProposalApplied:
		if p.ResolvedAt == nil || p.AppliedRevision == nil {
			return errors.New("applied proposal requires resolution and revision")
		}
		if err := p.AppliedRevision.Validate(); err != nil {
			return err
		}
		if *p.AppliedRevision <= p.BaseRevision {
			return errors.New("applied revision must follow base revision")
		}
	case ProposalRejected, ProposalInvalidated:
		if p.ResolvedAt == nil || p.AppliedRevision != nil {
			return errors.New("closed proposal requires resolution without applied revision")
		}
	default:
		return errors.New("invalid proposal state")
	}
	if p.ResolvedAt != nil && p.ResolvedAt.Before(p.CreatedAt) {
		return errors.New("proposal resolution precedes creation")
	}
	return nil
}

func (p RouteProposal) Clone() RouteProposal {
	clone := p
	clone.Candidate = p.Candidate.Clone()
	clone.Changes = make([]RouteChange, len(p.Changes))
	for i, change := range p.Changes {
		clone.Changes[i] = change
		clone.Changes[i].BeforeVisitID = cloneVisitID(change.BeforeVisitID)
		clone.Changes[i].AfterVisitID = cloneVisitID(change.AfterVisitID)
		clone.Changes[i].TimeShiftSeconds = cloneInt64(change.TimeShiftSeconds)
		clone.Changes[i].LegPosition = cloneInt(change.LegPosition)
		clone.Changes[i].BeforeCost = cloneMoney(change.BeforeCost)
		clone.Changes[i].AfterCost = cloneMoney(change.AfterCost)
		if change.BeforeVerification != nil {
			value := *change.BeforeVerification
			clone.Changes[i].BeforeVerification = &value
		}
		if change.AfterVerification != nil {
			value := *change.AfterVerification
			clone.Changes[i].AfterVerification = &value
		}
	}
	clone.Conflicts = make([]Conflict, len(p.Conflicts))
	for i, conflict := range p.Conflicts {
		clone.Conflicts[i] = conflict
		clone.Conflicts[i].VisitIDs = append([]VisitID(nil), conflict.VisitIDs...)
	}
	clone.EffectiveStartAt = cloneTime(p.EffectiveStartAt)
	clone.ResolvedAt = cloneTime(p.ResolvedAt)
	if p.AppliedRevision != nil {
		value := *p.AppliedRevision
		clone.AppliedRevision = &value
	}
	return clone
}
