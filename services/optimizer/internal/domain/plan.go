package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type ResultStatus string

const (
	ResultReady ResultStatus = "READY"
	// Feasible only under unknown conditions the user explicitly accepted.
	ResultPartial         ResultStatus = "PARTIAL"
	ResultNoFeasibleRoute ResultStatus = "NO_FEASIBLE_ROUTE"
	ResultConflict        ResultStatus = "CONFLICT"
)

type CatalogSnapshot struct {
	PlaceID             PlaceID
	EntranceID          *EntranceID
	EventID             *EventID
	SessionID           *SessionID
	Title               string
	Category            Category
	InterestMask        InterestMask
	Availability        Availability
	RegistrationDetails string
	AgeRequirements     string
	SessionStart        *time.Time
	SessionEnd          *time.Time
	SessionVersion      string
	DataMode            DataMode
	Provenance          Provenance
}

func (s CatalogSnapshot) Validate() error {
	if err := requireID(s.PlaceID, "place"); err != nil {
		return err
	}
	if err := optionalID(s.EntranceID, "entrance"); err != nil {
		return err
	}
	if err := optionalID(s.EventID, "event"); err != nil {
		return err
	}
	if err := optionalID(s.SessionID, "session"); err != nil {
		return err
	}
	if s.SessionID != nil && s.EventID == nil {
		return errors.New("catalog session requires an event")
	}
	if strings.TrimSpace(s.Title) == "" {
		return errors.New("catalog title is required")
	}
	if err := s.Category.Validate(); err != nil {
		return err
	}
	if err := s.Availability.Validate(); err != nil {
		return err
	}
	if (s.SessionStart == nil) != (s.SessionEnd == nil) {
		return errors.New("catalog session times must be given together")
	}
	if s.SessionStart != nil && (s.SessionStart.IsZero() || !s.SessionEnd.After(*s.SessionStart)) {
		return errors.New("catalog session interval is invalid")
	}
	if err := s.DataMode.Validate(); err != nil {
		return err
	}
	return s.Provenance.Validate()
}

type ConstraintStrength string

const (
	StrengthHard ConstraintStrength = "hard"
	StrengthSoft ConstraintStrength = "soft"
)

type ConstraintOutcome string

const (
	OutcomeSatisfied   ConstraintOutcome = "satisfied"
	OutcomeConditional ConstraintOutcome = "conditional"
)

type AppliedConstraint struct {
	Code     string
	Strength ConstraintStrength
	Outcome  ConstraintOutcome
	Message  string
}

func (c AppliedConstraint) Validate() error {
	if err := validateCodeMessage(c.Code, c.Message, "applied constraint"); err != nil {
		return err
	}
	if c.Strength != StrengthHard && c.Strength != StrengthSoft {
		return errors.New("invalid constraint strength")
	}
	if c.Outcome != OutcomeSatisfied && c.Outcome != OutcomeConditional {
		return errors.New("invalid constraint outcome")
	}
	return nil
}

type ParticipationEvidence string

const (
	EvidenceNone     ParticipationEvidence = "none"
	EvidenceUser     ParticipationEvidence = "user"
	EvidenceProvider ParticipationEvidence = "provider"
)

type Participation struct {
	Status   ParticipationStatus
	Evidence ParticipationEvidence
}

func (p Participation) Validate() error {
	if err := p.Status.Validate(); err != nil {
		return err
	}
	switch p.Evidence {
	case EvidenceNone, EvidenceUser, EvidenceProvider:
	default:
		return errors.New("invalid participation evidence")
	}
	if p.Status == ParticipationProviderConfirmed && p.Evidence != EvidenceProvider {
		return errors.New("provider confirmation requires provider evidence")
	}
	return nil
}

type StepKind string

const (
	StepVisit    StepKind = "visit"
	StepFreeTime StepKind = "free_time"
)

type Step struct {
	VisitID            VisitID
	Kind               StepKind
	Position           int
	ArrivalAt          time.Time
	VisitStartAt       time.Time
	VisitEndAt         time.Time
	DepartureAt        time.Time
	MinDuration        time.Duration
	Pinned             bool
	Obligation         bool
	Participation      Participation
	Catalog            *CatalogSnapshot
	Cost               *CostSnapshot
	AppliedConstraints []AppliedConstraint
}

func (s Step) Validate() error {
	if err := requireID(s.VisitID, "visit"); err != nil {
		return err
	}
	if s.Position < 1 || s.MinDuration < 0 {
		return errors.New("step position or minimum duration is invalid")
	}
	if s.ArrivalAt.IsZero() || s.VisitStartAt.Before(s.ArrivalAt) || !s.VisitEndAt.After(s.VisitStartAt) || s.DepartureAt.Before(s.VisitEndAt) {
		return errors.New("step interval is invalid")
	}
	if s.VisitEndAt.Sub(s.VisitStartAt) < s.MinDuration {
		return errors.New("step is shorter than its minimum duration")
	}
	switch s.Kind {
	case StepVisit:
		if s.Catalog == nil || s.Cost == nil || s.MinDuration == 0 {
			return errors.New("visit step requires catalog, cost and minimum duration")
		}
		if err := s.Catalog.Validate(); err != nil {
			return err
		}
		if err := s.Cost.Validate(); err != nil {
			return err
		}
	case StepFreeTime:
		if s.Catalog != nil || s.Cost != nil {
			return errors.New("free time step must not have catalog or cost")
		}
	default:
		return errors.New("invalid step kind")
	}
	if err := s.Participation.Validate(); err != nil {
		return err
	}
	for _, constraint := range s.AppliedConstraints {
		if err := constraint.Validate(); err != nil {
			return err
		}
	}
	return nil
}

type LegEndpoint string

const (
	EndpointOrigin      LegEndpoint = "origin"
	EndpointVisit       LegEndpoint = "visit"
	EndpointDestination LegEndpoint = "destination"
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
	return requireNonBlank(e.Limitations, "leg limitation")
}

type Leg struct {
	Position       int
	From           LegEndpoint
	To             LegEndpoint
	FromVisitID    *VisitID
	ToVisitID      *VisitID
	DepartureAt    time.Time
	ArrivalAt      time.Time
	Mode           MovementMode
	DistanceMeters *float64
	// Geometry never raises the verification status.
	Geometry     []Coordinate
	Verification VerificationStatus
	Evidence     LegEvidence
	Cost         CostSnapshot
}

func (l Leg) Validate() error {
	if l.Position < 1 || l.DepartureAt.IsZero() || l.ArrivalAt.Before(l.DepartureAt) {
		return errors.New("leg position or interval is invalid")
	}
	if err := l.Mode.Validate(); err != nil {
		return err
	}
	switch l.From {
	case EndpointOrigin:
		if l.FromVisitID != nil {
			return errors.New("leg from origin must not have a source visit")
		}
	case EndpointVisit:
		if err := requireEndpointVisit(l.FromVisitID); err != nil {
			return err
		}
	default:
		return errors.New("invalid leg source")
	}
	switch l.To {
	case EndpointDestination:
		if l.ToVisitID != nil {
			return errors.New("leg to destination must not have a target visit")
		}
	case EndpointVisit:
		if err := requireEndpointVisit(l.ToVisitID); err != nil {
			return err
		}
	default:
		return errors.New("invalid leg target")
	}
	if l.DistanceMeters != nil && (!finite(*l.DistanceMeters) || *l.DistanceMeters < 0) {
		return errors.New("leg distance must be finite and non-negative")
	}
	for _, point := range l.Geometry {
		if err := point.Validate(); err != nil {
			return err
		}
	}
	if err := l.Verification.Validate(); err != nil {
		return err
	}
	if err := l.Evidence.Validate(); err != nil {
		return err
	}
	return l.Cost.Validate()
}

func requireEndpointVisit(id *VisitID) error {
	if id == nil {
		return errors.New("visit endpoint requires a visit")
	}
	return requireID(*id, "visit")
}

type Scope string

const (
	ScopeRoute Scope = "route"
	ScopeVisit Scope = "visit"
	ScopeLeg   Scope = "leg"
)

type Warning struct {
	Code        string
	Scope       Scope
	VisitID     *VisitID
	LegPosition *int
	Message     string
}

func (w Warning) Validate() error {
	if err := validateCodeMessage(w.Code, w.Message, "warning"); err != nil {
		return err
	}
	switch w.Scope {
	case ScopeRoute:
		if w.VisitID != nil || w.LegPosition != nil {
			return errors.New("route warning must not target a visit or leg")
		}
	case ScopeVisit:
		if w.LegPosition != nil {
			return errors.New("visit warning must not target a leg")
		}
		return requireEndpointVisit(w.VisitID)
	case ScopeLeg:
		if w.VisitID != nil || w.LegPosition == nil || *w.LegPosition < 1 {
			return errors.New("leg warning requires only a positive leg position")
		}
	default:
		return errors.New("invalid warning scope")
	}
	return nil
}

// Conflict is an incompatibility between hard constraints, not a stale route revision.
type Conflict struct {
	Code     string
	VisitIDs []VisitID
	// Used when the conflicting obligation has no visit yet.
	SessionIDs []SessionID
	Message    string
}

func (c Conflict) Validate() error {
	if err := validateCodeMessage(c.Code, c.Message, "conflict"); err != nil {
		return err
	}
	for _, id := range c.VisitIDs {
		if err := requireID(id, "visit"); err != nil {
			return err
		}
	}
	for _, id := range c.SessionIDs {
		if err := requireID(id, "session"); err != nil {
			return err
		}
	}
	return nil
}

type Plan struct {
	Archetype       Archetype
	Start           time.Time
	End             time.Time
	Origin          Coordinate
	Destination     *Coordinate
	CatalogRevision CatalogRevision
	Result          ResultStatus
	Warnings        []Warning
	Conflicts       []Conflict
	Cost            CostSummary
	Geometry        []Coordinate
	Steps           []Step
	Legs            []Leg
}

func (p Plan) Validate() error {
	if err := p.Archetype.Validate(); err != nil {
		return err
	}
	if p.Start.IsZero() || !p.End.After(p.Start) {
		return errors.New("plan interval is invalid")
	}
	if err := p.Origin.Validate(); err != nil {
		return err
	}
	if p.Destination != nil {
		if err := p.Destination.Validate(); err != nil {
			return err
		}
	}
	if err := p.CatalogRevision.Validate(); err != nil {
		return err
	}
	if err := p.validateResult(); err != nil {
		return err
	}
	if err := p.Cost.Validate(); err != nil {
		return err
	}
	for _, point := range p.Geometry {
		if err := point.Validate(); err != nil {
			return err
		}
	}
	for _, conflict := range p.Conflicts {
		if err := conflict.Validate(); err != nil {
			return err
		}
	}
	if err := p.validateSteps(); err != nil {
		return err
	}
	if err := p.validateLegs(); err != nil {
		return err
	}
	return p.validateWarnings()
}

func (p Plan) validateResult() error {
	switch p.Result {
	case ResultReady, ResultPartial:
		if len(p.Steps) == 0 {
			return errors.New("feasible plan requires steps")
		}
	case ResultNoFeasibleRoute, ResultConflict:
		if len(p.Steps) > 0 {
			return errors.New("infeasible plan must not have steps")
		}
		if p.Result == ResultConflict && len(p.Conflicts) == 0 {
			return errors.New("conflict result requires a conflict")
		}
	default:
		return errors.New("invalid result status")
	}
	return nil
}

func (p Plan) validateSteps() error {
	currency := p.Cost.KnownPersonal.Currency
	visits := make(map[VisitID]struct{}, len(p.Steps))
	for i, step := range p.Steps {
		if err := step.Validate(); err != nil {
			return fmt.Errorf("step %d: %w", i+1, err)
		}
		if step.Position != i+1 {
			return errors.New("step positions must run from 1 without gaps")
		}
		if _, duplicate := visits[step.VisitID]; duplicate {
			return errors.New("plan contains a duplicate visit")
		}
		visits[step.VisitID] = struct{}{}
		// Legs already keep steps ordered and after the plan start; without a
		// destination leg only the end of the last step is left to check.
		if step.DepartureAt.After(p.End) {
			return errors.New("step ends after the plan interval")
		}
		if step.Cost != nil && step.Cost.Price.Currency != currency {
			return errors.New("step cost must use the route currency")
		}
	}
	return nil
}

func (p Plan) validateLegs() error {
	want := 0
	if len(p.Steps) > 0 {
		want = len(p.Steps)
		if p.Destination != nil {
			want++
		}
	}
	if len(p.Legs) != want {
		return fmt.Errorf("plan requires %d legs, got %d", want, len(p.Legs))
	}
	for i, leg := range p.Legs {
		if err := leg.Validate(); err != nil {
			return fmt.Errorf("leg %d: %w", i+1, err)
		}
		if leg.Position != i+1 {
			return errors.New("leg positions must run from 1 without gaps")
		}
		if leg.Cost.Price.Currency != p.Cost.KnownPersonal.Currency {
			return errors.New("leg cost must use the route currency")
		}
		if err := p.validateLegEnds(i, leg); err != nil {
			return fmt.Errorf("leg %d: %w", i+1, err)
		}
	}
	return nil
}

// validateLegEnds checks that leg i connects the end of the previous stop to the next one in time.
func (p Plan) validateLegEnds(i int, leg Leg) error {
	earliest := p.Start
	if i == 0 {
		if leg.From != EndpointOrigin {
			return errors.New("first leg must start at the origin")
		}
	} else {
		previous := p.Steps[i-1]
		if leg.From != EndpointVisit || *leg.FromVisitID != previous.VisitID {
			return errors.New("leg must start at the previous visit")
		}
		earliest = previous.DepartureAt
	}
	if leg.DepartureAt.Before(earliest) {
		return errors.New("leg departs before the previous stop ends")
	}
	if i == len(p.Steps) {
		if leg.To != EndpointDestination {
			return errors.New("last leg must end at the destination")
		}
		if leg.ArrivalAt.After(p.End) {
			return errors.New("destination is reached after the plan ends")
		}
		return nil
	}
	next := p.Steps[i]
	if leg.To != EndpointVisit || *leg.ToVisitID != next.VisitID {
		return errors.New("leg must end at the next visit")
	}
	if leg.ArrivalAt.After(next.ArrivalAt) {
		return errors.New("leg arrives after the next visit's arrival")
	}
	return nil
}

func (p Plan) validateWarnings() error {
	for _, warning := range p.Warnings {
		if err := warning.Validate(); err != nil {
			return err
		}
		if warning.VisitID != nil && !p.hasVisit(*warning.VisitID) {
			return errors.New("warning targets a visit outside the plan")
		}
		if warning.LegPosition != nil && *warning.LegPosition > len(p.Legs) {
			return errors.New("warning targets a leg outside the plan")
		}
	}
	return nil
}

func (p Plan) hasVisit(id VisitID) bool {
	for _, step := range p.Steps {
		if step.VisitID == id {
			return true
		}
	}
	return false
}

// ValidateBudget checks the cost conclusion against the route budget; an unknown
// component never lets a strict budget be reported as resolved.
func (p Plan) ValidateBudget(b Budget) error {
	if err := b.Validate(); err != nil {
		return err
	}
	if b.Limit != nil && b.Limit.Currency != p.Cost.KnownPersonal.Currency {
		return errors.New("budget and route cost currencies differ")
	}
	conclusion := p.Cost.BudgetConclusion
	if b.Mode == BudgetNone && conclusion != BudgetNotApplicable {
		return errors.New("route without a budget must not conclude on it")
	}
	if b.Mode != BudgetNone && conclusion == BudgetNotApplicable && len(p.Steps) > 0 {
		return errors.New("route with a budget must conclude on it")
	}
	if b.Mode == BudgetStrict && len(p.Cost.UnknownComponents) > 0 && conclusion != BudgetUnknown {
		return errors.New("strict budget with unknown costs must stay unknown")
	}
	return nil
}
