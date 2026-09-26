package domain

import (
	"errors"
	"strings"
	"time"
)

type ResultStatus string

const (
	ResultReady           ResultStatus = "READY"
	ResultPartial         ResultStatus = "PARTIAL"
	ResultNoFeasibleRoute ResultStatus = "NO_FEASIBLE_ROUTE"
	ResultConflict        ResultStatus = "CONFLICT"
)

type WarningScope string

const (
	WarningRoute WarningScope = "route"
	WarningVisit WarningScope = "visit"
	WarningLeg   WarningScope = "leg"
)

type Warning struct {
	Code        string
	Scope       WarningScope
	VisitID     *VisitID
	LegPosition *int
	Message     string
}

func (w Warning) Validate() error {
	if strings.TrimSpace(w.Code) == "" || strings.TrimSpace(w.Message) == "" {
		return errors.New("warning code and message are required")
	}
	switch w.Scope {
	case WarningRoute:
		if w.VisitID != nil || w.LegPosition != nil {
			return errors.New("route warning must not identify a visit or leg")
		}
	case WarningVisit:
		if w.VisitID == nil || w.LegPosition != nil {
			return errors.New("visit warning requires only a visit")
		}
		if err := requiredID([16]byte(*w.VisitID)); err != nil {
			return err
		}
	case WarningLeg:
		if w.LegPosition == nil || *w.LegPosition <= 0 || w.VisitID != nil {
			return errors.New("leg warning requires only a positive leg position")
		}
	default:
		return errors.New("invalid warning scope")
	}
	return nil
}

type Conflict struct {
	Code     string
	VisitIDs []VisitID
	Message  string
}

func (c Conflict) Validate() error {
	if strings.TrimSpace(c.Code) == "" || strings.TrimSpace(c.Message) == "" {
		return errors.New("conflict code and message are required")
	}
	for _, visitID := range c.VisitIDs {
		if err := requiredID([16]byte(visitID)); err != nil {
			return err
		}
	}
	return nil
}

type RoutePlanSnapshot struct {
	SchemaVersion   int
	Lifecycle       RouteLifecycle
	ArchetypeID     string
	Timezone        string
	StartAt         time.Time
	EndAt           time.Time
	Origin          Coordinate
	Destination     *Coordinate
	Constraints     RouteConstraints
	CatalogRevision CatalogRevision
	Result          ResultStatus
	Warnings        []Warning
	Conflicts       []Conflict
	Cost            CostSummary
	Geometry        []Coordinate
	Steps           []RouteStep
	Legs            []RouteLeg
}

func (s RoutePlanSnapshot) Validate() error {
	if s.SchemaVersion <= 0 {
		return errors.New("snapshot schema version must be positive")
	}
	if s.Lifecycle != RouteDraft && s.Lifecycle != RouteSaved {
		return errors.New("invalid snapshot lifecycle")
	}
	if strings.TrimSpace(s.ArchetypeID) == "" || strings.TrimSpace(s.Timezone) == "" {
		return errors.New("snapshot archetype and timezone are required")
	}
	if s.StartAt.IsZero() || !s.EndAt.After(s.StartAt) {
		return errors.New("snapshot planning interval is invalid")
	}
	if err := s.Origin.Validate(); err != nil {
		return err
	}
	if s.Destination != nil {
		if err := s.Destination.Validate(); err != nil {
			return err
		}
	}
	if err := s.Constraints.Validate(); err != nil {
		return err
	}
	if err := s.CatalogRevision.Validate(); err != nil {
		return err
	}
	switch s.Result {
	case ResultReady, ResultPartial, ResultNoFeasibleRoute, ResultConflict:
	default:
		return errors.New("invalid route result status")
	}
	for _, warning := range s.Warnings {
		if err := warning.Validate(); err != nil {
			return err
		}
	}
	for _, conflict := range s.Conflicts {
		if err := conflict.Validate(); err != nil {
			return err
		}
	}
	if err := s.Cost.Validate(); err != nil {
		return err
	}
	if s.Constraints.Budget.Limit != nil && s.Constraints.Budget.Limit.Currency != s.Cost.KnownPersonal.Currency {
		return errors.New("budget and route cost currencies must match")
	}
	if s.Constraints.ProgramBalance != nil && s.Constraints.ProgramBalance.Balance.Currency != s.Cost.KnownPersonal.Currency {
		return errors.New("program balance and route cost currencies must match")
	}
	switch s.Constraints.Budget.Mode {
	case BudgetNone:
		if s.Cost.BudgetConclusion != BudgetNotApplicable {
			return errors.New("route without budget must use not applicable conclusion")
		}
	case BudgetStrict:
		if len(s.Cost.UnknownComponents) > 0 && s.Cost.BudgetConclusion != BudgetUnknown {
			return errors.New("unknown strict budget must not be reported as resolved")
		}
	}
	for _, point := range s.Geometry {
		if err := point.Validate(); err != nil {
			return err
		}
	}
	visits := make(map[VisitID]struct{}, len(s.Steps))
	stepPositions := make(map[int]struct{}, len(s.Steps))
	for _, step := range s.Steps {
		if err := step.Validate(); err != nil {
			return err
		}
		if _, exists := visits[step.VisitID]; exists {
			return errors.New("snapshot contains duplicate visit")
		}
		if _, exists := stepPositions[step.Position]; exists {
			return errors.New("snapshot contains duplicate step position")
		}
		visits[step.VisitID] = struct{}{}
		stepPositions[step.Position] = struct{}{}
		if step.Cost != nil && step.Cost.Price.Currency != s.Cost.KnownPersonal.Currency {
			return errors.New("step and route cost currencies must match")
		}
	}
	legPositions := make(map[int]struct{}, len(s.Legs))
	for _, leg := range s.Legs {
		if err := leg.Validate(); err != nil {
			return err
		}
		if _, exists := legPositions[leg.Position]; exists {
			return errors.New("snapshot contains duplicate leg position")
		}
		legPositions[leg.Position] = struct{}{}
		if leg.Cost.Price.Currency != s.Cost.KnownPersonal.Currency {
			return errors.New("leg and route cost currencies must match")
		}
		for _, visitID := range []*VisitID{leg.FromVisitID, leg.ToVisitID} {
			if visitID != nil {
				if _, exists := visits[*visitID]; !exists {
					return errors.New("route leg references a visit outside the snapshot")
				}
			}
		}
	}
	return nil
}

type RouteMutationKind string

const (
	MutationCreate        RouteMutationKind = "create"
	MutationSave          RouteMutationKind = "save"
	MutationApply         RouteMutationKind = "apply"
	MutationPin           RouteMutationKind = "pin"
	MutationDelete        RouteMutationKind = "delete"
	MutationParticipation RouteMutationKind = "participation"
	MutationExecution     RouteMutationKind = "execution"
)

type RouteRevision struct {
	RouteID   RouteID
	Number    RouteRevisionNumber
	Parent    *RouteRevisionNumber
	Plan      RoutePlanSnapshot
	Mutation  RouteMutationKind
	CreatedAt time.Time
}

func (r RouteRevision) Validate() error {
	if err := requiredID([16]byte(r.RouteID)); err != nil {
		return err
	}
	if err := r.Number.Validate(); err != nil {
		return err
	}
	if r.Parent != nil {
		if err := r.Parent.Validate(); err != nil {
			return err
		}
		if *r.Parent >= r.Number {
			return errors.New("parent revision must precede revision")
		}
	}
	if err := r.Plan.Validate(); err != nil {
		return err
	}
	switch r.Mutation {
	case MutationCreate, MutationSave, MutationApply, MutationPin, MutationDelete, MutationParticipation, MutationExecution:
	default:
		return errors.New("invalid route mutation kind")
	}
	if r.CreatedAt.IsZero() {
		return errors.New("route revision creation time is required")
	}
	return nil
}

func (s RoutePlanSnapshot) Clone() RoutePlanSnapshot {
	clone := s
	clone.Destination = cloneCoordinate(s.Destination)
	clone.Constraints = s.Constraints.clone()
	clone.Warnings = append([]Warning(nil), s.Warnings...)
	for i := range clone.Warnings {
		clone.Warnings[i].VisitID = cloneVisitID(s.Warnings[i].VisitID)
		clone.Warnings[i].LegPosition = cloneInt(s.Warnings[i].LegPosition)
	}
	clone.Conflicts = make([]Conflict, len(s.Conflicts))
	for i, conflict := range s.Conflicts {
		clone.Conflicts[i] = conflict
		clone.Conflicts[i].VisitIDs = append([]VisitID(nil), conflict.VisitIDs...)
	}
	clone.Cost = s.Cost.clone()
	clone.Geometry = append([]Coordinate(nil), s.Geometry...)
	clone.Steps = make([]RouteStep, len(s.Steps))
	for i, step := range s.Steps {
		clone.Steps[i] = step.clone()
	}
	clone.Legs = make([]RouteLeg, len(s.Legs))
	for i, leg := range s.Legs {
		clone.Legs[i] = leg.clone()
	}
	return clone
}

func (c RouteConstraints) clone() RouteConstraints {
	clone := c
	clone.Budget.Limit = cloneMoney(c.Budget.Limit)
	clone.ExcludedCategories = append([]string(nil), c.ExcludedCategories...)
	clone.MovementModes = append([]MovementMode(nil), c.MovementModes...)
	clone.BenefitPrograms = append([]string(nil), c.BenefitPrograms...)
	if c.ProgramBalance != nil {
		balance := *c.ProgramBalance
		clone.ProgramBalance = &balance
	}
	clone.AudienceClaims = append([]AudienceClaim(nil), c.AudienceClaims...)
	clone.Obligations = append([]RouteObligation(nil), c.Obligations...)
	for i := range clone.Obligations {
		clone.Obligations[i].VisitID = cloneVisitID(c.Obligations[i].VisitID)
		clone.Obligations[i].SessionID = cloneSessionID(c.Obligations[i].SessionID)
		clone.Obligations[i].StartsAt = cloneTime(c.Obligations[i].StartsAt)
	}
	clone.SoftPreferences = append([]string(nil), c.SoftPreferences...)
	if c.LunchWindow != nil {
		window := *c.LunchWindow
		clone.LunchWindow = &window
	}
	clone.AcceptedUnknowns = append([]UnknownConditionCode(nil), c.AcceptedUnknowns...)
	return clone
}

func (s RouteStep) clone() RouteStep {
	clone := s
	if s.Catalog != nil {
		catalog := s.Catalog.clone()
		clone.Catalog = &catalog
	}
	if s.Cost != nil {
		cost := s.Cost.clone()
		clone.Cost = &cost
	}
	clone.AppliedConstraints = append([]AppliedConstraint(nil), s.AppliedConstraints...)
	return clone
}

func (s CatalogSnapshot) clone() CatalogSnapshot {
	clone := s
	clone.PlaceID = clonePlaceID(s.PlaceID)
	clone.EventID = cloneEventID(s.EventID)
	clone.SessionID = cloneSessionID(s.SessionID)
	clone.SessionStartsAt = cloneTime(s.SessionStartsAt)
	clone.SessionEndsAt = cloneTime(s.SessionEndsAt)
	clone.Provenance = s.Provenance.clone()
	return clone
}

func (l RouteLeg) clone() RouteLeg {
	clone := l
	clone.FromVisitID = cloneVisitID(l.FromVisitID)
	clone.ToVisitID = cloneVisitID(l.ToVisitID)
	if l.DistanceMeters != nil {
		distance := *l.DistanceMeters
		clone.DistanceMeters = &distance
	}
	clone.Geometry = append([]Coordinate(nil), l.Geometry...)
	clone.Evidence.Limitations = append([]string(nil), l.Evidence.Limitations...)
	clone.Cost = l.Cost.clone()
	return clone
}

func (s CostSnapshot) clone() CostSnapshot {
	clone := s
	clone.PriceOfferID = clonePriceOfferID(s.PriceOfferID)
	clone.Price.LowerMinor = cloneInt64(s.Price.LowerMinor)
	clone.Price.UpperMinor = cloneInt64(s.Price.UpperMinor)
	clone.PersonalAmount = cloneMoney(s.PersonalAmount)
	clone.ProgramAmount = cloneMoney(s.ProgramAmount)
	clone.UnknownComponents = append([]UnknownCostComponent(nil), s.UnknownComponents...)
	clone.Provenance = s.Provenance.clone()
	return clone
}

func (s CostSummary) clone() CostSummary {
	clone := s
	clone.TotalLower = cloneMoney(s.TotalLower)
	clone.TotalUpper = cloneMoney(s.TotalUpper)
	clone.UnknownComponents = append([]UnknownCostComponent(nil), s.UnknownComponents...)
	return clone
}

func (p FactProvenance) clone() FactProvenance {
	clone := p
	if p.SourceURL != nil {
		value := *p.SourceURL
		clone.SourceURL = &value
	}
	if p.SourceRecordID != nil {
		value := *p.SourceRecordID
		clone.SourceRecordID = &value
	}
	clone.SourceUpdatedAt = cloneTime(p.SourceUpdatedAt)
	clone.VerifiedAt = cloneTime(p.VerifiedAt)
	return clone
}

func cloneMoney(value *Money) *Money {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneCoordinate(value *Coordinate) *Coordinate {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneVisitID(value *VisitID) *VisitID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneSessionID(value *EventSessionID) *EventSessionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func clonePlaceID(value *PlaceID) *PlaceID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneEventID(value *EventID) *EventID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func clonePriceOfferID(value *PriceOfferID) *PriceOfferID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
