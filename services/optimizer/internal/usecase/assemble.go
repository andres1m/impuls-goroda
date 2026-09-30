package usecase

import (
	"slices"
	"strconv"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/pricing"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/validation"
)

var unestimatedFare = domain.UnknownCostComponent{
	Code:    "TRANSPORT_FARE_NOT_ESTIMATED",
	Message: "The fare is not estimated; travel costs are outside the budget",
}

// assemble turns a searched route into a plan and returns what the plan was built from, for validation.
func (p *Planner) assemble(
	req *domain.OptimizeRequest,
	policy *pricing.Policy,
	r route,
	degraded bool,
	data domain.DataFreshness,
) (domain.Plan, validation.Input, error) {
	visits := r.branch.Visits
	candidates := make([]*domain.Candidate, len(visits))
	for i := range visits {
		candidates[i] = visits[i].Candidate
	}
	costs, summary, err := policy.Cost(candidates)
	if err != nil {
		return domain.Plan{}, validation.Input{}, err
	}
	if lunch := r.branch.Lunch; lunch != nil && lunch.Venue {
		ticketCosts := append(slices.Clone(costs[:lunch.At]), costs[lunch.At+1:]...)
		summary, err = policy.Summarize(ticketCosts)
		if err != nil {
			return domain.Plan{}, validation.Input{}, err
		}
	}
	plan := domain.Plan{
		Archetype: r.archetype, Start: req.Start, End: req.End, Origin: req.Origin, Destination: req.Destination,
		CatalogRevision: data.CatalogRevision, Result: domain.ResultReady, Cost: summary,
		Geometry: []domain.Coordinate{req.Origin},
	}
	in := validation.Input{
		Constraints: req.Constraints, Currency: policy.Currency, Degraded: degraded,
		Candidates: make(map[domain.VisitID]domain.Candidate, len(visits)),
	}
	state := &assembleState{
		planner:   p,
		req:       req,
		policy:    policy,
		route:     r,
		degraded:  degraded,
		plan:      &plan,
		departure: req.Start,
	}
	unknownPrice := false
	for i := range visits {
		v := &visits[i]
		state.pausesAt(i)
		id := p.newID()
		c := v.Candidate
		in.Candidates[id] = *c
		state.addVisit(i, v, id, &costs[i])
		if costs[i].Price.Status == domain.PriceUnknown && (r.branch.Lunch == nil || !r.branch.Lunch.Venue || r.branch.Lunch.At != i) {
			unknownPrice = true
		}
	}
	state.pausesAt(len(visits))
	state.finalize(unknownPrice)
	return plan, in, nil
}

type assembleState struct {
	planner   *Planner
	req       *domain.OptimizeRequest
	policy    *pricing.Policy
	route     route
	degraded  bool
	plan      *domain.Plan
	departure time.Time
	from      *domain.VisitID
}

func (s *assembleState) pause(lunch *domain.Lunch) {
	id := s.planner.newID()
	s.plan.Legs = append(
		s.plan.Legs,
		stay(
			len(s.plan.Legs)+1,
			s.from,
			&id,
			s.departure,
			s.plan.Geometry[len(s.plan.Geometry)-1],
			s.req.Constraints.MovementModes[0],
			s.degraded,
			s.policy.Currency,
		),
	)
	s.plan.Steps = append(s.plan.Steps, domain.Step{
		VisitID: id, Kind: domain.StepFreeTime, Position: len(s.plan.Steps) + 1,
		ArrivalAt: s.departure, VisitStartAt: lunch.StartAt, VisitEndAt: lunch.EndAt, DepartureAt: lunch.EndAt,
		Participation: domain.Participation{Status: domain.ParticipationNotRequired, Evidence: domain.EvidenceNone},
		AppliedConstraints: []domain.AppliedConstraint{{
			Code: lunchWindowCode, Strength: domain.StrengthSoft, Outcome: domain.OutcomeSatisfied,
			Message: "The lunch time is kept free",
		}},
	})
	s.plan.Warnings = append(s.plan.Warnings, domain.Warning{
		Code:    "LUNCH_NO_VENUE",
		Scope:   domain.ScopeVisit,
		VisitID: &id,
		Message: "No place to eat within 1 km fits the lunch time and the route's conditions; the lunch time is left free with nothing booked",
	})
	s.departure, s.from = lunch.EndAt, &id
}

func (s *assembleState) rest(x domain.Rest) {
	id := s.planner.newID()
	s.plan.Legs = append(
		s.plan.Legs,
		stay(
			len(s.plan.Legs)+1,
			s.from,
			&id,
			s.departure,
			s.plan.Geometry[len(s.plan.Geometry)-1],
			s.req.Constraints.MovementModes[0],
			s.degraded,
			s.policy.Currency,
		),
	)
	step := restStep(id, x)
	step.Position, step.ArrivalAt = len(s.plan.Steps)+1, s.departure
	s.plan.Steps = append(s.plan.Steps, step)
	s.departure, s.from = x.EndAt, &id
}

// pausesAt writes the lunch pause and the rests due before visit i in the order they happen.
func (s *assembleState) pausesAt(i int) {
	lunch := s.route.branch.Lunch
	rests := slices.DeleteFunc(slices.Clone(s.route.branch.Rests), func(x domain.Rest) bool { return x.At != i })
	lunchHere := lunch != nil && !lunch.Venue && lunch.At == i
	for _, x := range rests {
		if lunchHere && lunch.StartAt.Before(x.StartAt) {
			s.pause(lunch)
			lunchHere = false
		}
		s.rest(x)
	}
	if lunchHere {
		s.pause(lunch)
	}
}

func (s *assembleState) addVisit(i int, v *domain.SearchVisit, id domain.VisitID, cost *domain.CostSnapshot) {
	c := v.Candidate
	obligation := obligationFor(s.req.Constraints.Obligations, c)
	s.plan.Legs = append(
		s.plan.Legs,
		leg(
			len(s.plan.Legs)+1,
			s.from,
			&id,
			s.departure,
			v.ArrivalAt,
			&v.Transit,
			s.plan.Geometry[len(s.plan.Geometry)-1],
			c.Place.Location,
			s.policy.Currency,
		),
	)
	step := domain.Step{
		VisitID: id, Kind: domain.StepVisit, Position: len(s.plan.Steps) + 1,
		ArrivalAt: v.ArrivalAt, VisitStartAt: v.StartAt, VisitEndAt: v.EndAt, DepartureAt: v.EndAt,
		MinDuration:        c.Window.MinDuration,
		Obligation:         obligation != nil,
		Participation:      participation(c, obligation),
		Catalog:            snapshot(c),
		Cost:               cost,
		AppliedConstraints: softConstraints(c, s.route.archetype, s.req.Constraints.InterestMask),
	}
	if obligation != nil {
		// No transition is verified yet, so reaching a committed session is always an estimate.
		step.AppliedConstraints = append(step.AppliedConstraints, domain.AppliedConstraint{
			Code: obligationReachable, Strength: domain.StrengthHard, Outcome: domain.OutcomeConditional,
			Message: "The session is reachable by the estimated travel time; allow extra time",
		})
		s.plan.Warnings = append(s.plan.Warnings, domain.Warning{
			Code: "UNVERIFIED_TRANSITION", Scope: domain.ScopeVisit, VisitID: &id,
			Message: "Travel to this committed session is estimated, not verified",
		})
	}
	lunch := s.route.branch.Lunch
	if lunch != nil && lunch.Venue && lunch.At == i {
		if cost.Price.Status == domain.PriceUnknown {
			s.plan.Warnings = append(s.plan.Warnings, domain.Warning{
				Code: "LUNCH_PRICE_UNKNOWN", Scope: domain.ScopeVisit, VisitID: &id,
				Message: "The meal price is unknown; check it before visiting",
			})
		}
		step.AppliedConstraints = append(step.AppliedConstraints, domain.AppliedConstraint{
			Code: lunchWindowCode, Strength: domain.StrengthSoft, Outcome: domain.OutcomeSatisfied,
			Message: "Lunch at a place to eat near the route",
		})
		if c.Window.HoursUnknown {
			s.plan.Warnings = append(s.plan.Warnings, domain.Warning{
				Code: "OPENING_HOURS_UNKNOWN", Scope: domain.ScopeVisit, VisitID: &id,
				Message: "Opening hours of this place are unknown; check them before the visit",
			})
		}
	}
	s.plan.Steps = append(s.plan.Steps, step)
	s.plan.Geometry = append(s.plan.Geometry, c.Place.Location)
	s.departure, s.from = v.EndAt, &id
}

func (s *assembleState) finalize(unknownPrice bool) {
	if s.req.Destination != nil && s.route.branch.Finish != nil {
		finish := *s.route.branch.Finish
		s.plan.Legs = append(
			s.plan.Legs,
			leg(
				len(s.plan.Legs)+1,
				s.from,
				nil,
				s.departure,
				s.departure.Add(finish.Duration),
				&finish,
				s.plan.Geometry[len(s.plan.Geometry)-1],
				*s.req.Destination,
				s.policy.Currency,
			),
		)
		s.plan.Geometry = append(s.plan.Geometry, *s.req.Destination)
	}
	if slices.ContainsFunc(s.plan.Legs, func(l domain.Leg) bool { return l.Mode != domain.MovementWalk }) {
		s.plan.Warnings = append(s.plan.Warnings, domain.Warning{
			Code: "TRANSPORT_COST_NOT_INCLUDED", Scope: domain.ScopeRoute,
			Message: "Public transport and car costs are not included in the route cost",
		})
	}
	if s.degraded {
		s.plan.Warnings = append(s.plan.Warnings, degradedWarning())
	}
	cons := s.req.Constraints
	if unknownPrice && (cons.Budget.Mode == domain.BudgetStrict || cons.PushkinCardOnly) {
		s.plan.Result = domain.ResultPartial
	}
}

const (
	restBreak           = "REST_BREAK"
	lunchWindowCode     = "LUNCH_WINDOW"
	obligationReachable = "OBLIGATION_REACHABLE"
)

// restStep is the free time a rest keeps; the caller places it in the plan.
func restStep(id domain.VisitID, x domain.Rest) domain.Step {
	return domain.Step{
		VisitID: id, Kind: domain.StepFreeTime,
		ArrivalAt: x.StartAt, VisitStartAt: x.StartAt, VisitEndAt: x.EndAt, DepartureAt: x.EndAt,
		Participation: domain.Participation{Status: domain.ParticipationNotRequired, Evidence: domain.EvidenceNone},
		AppliedConstraints: []domain.AppliedConstraint{{
			Code: restBreak, Strength: domain.StrengthSoft, Outcome: domain.OutcomeSatisfied,
			Message: "A short rest keeps the day at the chosen pace",
		}},
	}
}

func isRestStep(s *domain.Step) bool {
	return s.Kind == domain.StepFreeTime &&
		slices.ContainsFunc(s.AppliedConstraints, func(c domain.AppliedConstraint) bool { return c.Code == restBreak })
}

// leg joins two stops; a nil visit is the origin before the first stop or the destination after the last.
func leg(
	position int,
	from, to *domain.VisitID,
	departure, arrival time.Time,
	t *domain.TransitEstimate,
	a, b domain.Coordinate,
	currency string,
) domain.Leg {
	distance := t.DistanceMeters
	l := domain.Leg{
		Position: position, From: domain.EndpointVisit, To: domain.EndpointVisit, FromVisitID: from, ToVisitID: to,
		DepartureAt: departure, ArrivalAt: arrival, Mode: t.Mode, DistanceMeters: &distance,
		// Straight between the stops: the line only shows the order and never raises the status.
		Geometry:     []domain.Coordinate{a, b},
		Verification: t.Verification, Evidence: t.Evidence, Cost: fare(t, currency),
	}
	if from == nil {
		l.From = domain.EndpointOrigin
	}
	if to == nil {
		l.To = domain.EndpointDestination
	}
	return l
}

// fare is free for walking; other fares are not estimated, and travel stays outside the budget.
func fare(t *domain.TransitEstimate, currency string) domain.CostSnapshot {
	provenance := domain.Provenance{SourceName: t.Evidence.Provider, FetchedAt: t.Evidence.ObservedAt}
	if t.Mode == domain.MovementWalk {
		zero := int64(0)
		return domain.CostSnapshot{
			Price: domain.Price{
				Status:     domain.PriceFree,
				Currency:   currency,
				LowerMinor: &zero,
				UpperMinor: &zero,
			},
			Provenance: provenance,
		}
	}
	return domain.CostSnapshot{
		Price:             domain.Price{Status: domain.PriceUnknown, Currency: currency},
		UnknownComponents: []domain.UnknownCostComponent{unestimatedFare},
		Provenance:        provenance,
	}
}

func obligationFor(obligations []domain.Obligation, c *domain.Candidate) *domain.Obligation {
	if c.Session == nil {
		return nil
	}
	i := slices.IndexFunc(
		obligations,
		func(o domain.Obligation) bool { return o.SessionID != nil && *o.SessionID == c.Session.ID },
	)
	if i < 0 {
		return nil
	}
	return &obligations[i]
}

func participation(c *domain.Candidate, o *domain.Obligation) domain.Participation {
	if o != nil {
		evidence := domain.EvidenceNone
		switch o.Participation {
		case domain.ParticipationUserReported:
			evidence = domain.EvidenceUser
		case domain.ParticipationProviderConfirmed:
			evidence = domain.EvidenceProvider
		case domain.ParticipationNotRequired,
			domain.ParticipationActionRequired,
			domain.ParticipationUnavailable:
		}
		return domain.Participation{Status: o.Participation, Evidence: evidence}
	}
	if c.Session == nil || c.Session.Access == domain.AccessFree {
		return domain.Participation{Status: domain.ParticipationNotRequired, Evidence: domain.EvidenceNone}
	}
	return domain.Participation{Status: domain.ParticipationActionRequired, Evidence: domain.EvidenceNone}
}

func snapshot(c *domain.Candidate) *domain.CatalogSnapshot {
	s := &domain.CatalogSnapshot{
		PlaceID: c.Place.ID, Title: c.Place.Title, Category: c.Category(), InterestMask: c.InterestMask(),
		// Opening hours are the only fact behind a visit without a session.
		Availability: domain.AvailabilityAvailable, DataMode: c.DataMode(), Provenance: c.Place.Provenance,
	}
	if c.Session != nil {
		eventID, sessionID := c.Event.ID, c.Session.ID
		start, end := c.Window.Start, c.Window.End
		s.EventID, s.SessionID = &eventID, &sessionID
		s.Title, s.Provenance = c.Event.Title, c.Session.Provenance
		s.Availability = c.Session.Availability
		s.SessionStart, s.SessionEnd = &start, &end
		s.SessionVersion = strconv.FormatInt(c.Session.Version, 10)
	}
	return s
}

func softConstraints(
	c *domain.Candidate,
	archetype domain.Archetype,
	interests domain.InterestMask,
) []domain.AppliedConstraint {
	var out []domain.AppliedConstraint
	if c.InterestMask().Matches(archetype.Mask()) > 0 {
		out = append(out, domain.AppliedConstraint{
			Code: "ARCHETYPE_MATCH", Strength: domain.StrengthSoft, Outcome: domain.OutcomeSatisfied,
			Message: "The visit matches the route archetype",
		})
	}
	if !interests.IsEmpty() && c.InterestMask().Matches(interests) > 0 {
		out = append(out, domain.AppliedConstraint{
			Code: "INTEREST_MATCH", Strength: domain.StrengthSoft, Outcome: domain.OutcomeSatisfied,
			Message: "The visit matches the requested interests",
		})
	}
	return out
}
