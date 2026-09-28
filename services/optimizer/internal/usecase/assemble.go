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
func (p *Planner) assemble(req domain.OptimizeRequest, policy pricing.Policy, r route, degraded bool, data domain.DataFreshness) (domain.Plan, validation.Input, error) {
	visits := r.branch.Visits
	candidates := make([]*domain.Candidate, len(visits))
	for i, v := range visits {
		candidates[i] = v.Candidate
	}
	costs, summary, err := policy.Cost(candidates)
	if err != nil {
		return domain.Plan{}, validation.Input{}, err
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
	departure, from := req.Start, (*domain.VisitID)(nil)
	lunch := r.branch.Lunch
	pause := func() {
		id := p.newID()
		plan.Legs = append(plan.Legs, stay(len(plan.Legs)+1, from, &id, departure, plan.Geometry[len(plan.Geometry)-1], req.Constraints.MovementModes[0], degraded, policy.Currency))
		plan.Steps = append(plan.Steps, domain.Step{
			VisitID: id, Kind: domain.StepFreeTime, Position: len(plan.Steps) + 1,
			ArrivalAt: departure, VisitStartAt: lunch.StartAt, VisitEndAt: lunch.EndAt, DepartureAt: lunch.EndAt,
			Participation: domain.Participation{Status: domain.ParticipationNotRequired, Evidence: domain.EvidenceNone},
			AppliedConstraints: []domain.AppliedConstraint{{
				Code: "LUNCH_WINDOW", Strength: domain.StrengthSoft, Outcome: domain.OutcomeSatisfied,
				Message: "The lunch time is kept free",
			}},
		})
		plan.Warnings = append(plan.Warnings, domain.Warning{
			Code: "LUNCH_NO_VENUE", Scope: domain.ScopeVisit, VisitID: &id,
			Message: "No place to eat within 1 km fits the lunch time and the route's conditions; the lunch time is left free with nothing booked",
		})
		departure, from = lunch.EndAt, &id
	}
	rest := func(x domain.Rest) {
		id := p.newID()
		plan.Legs = append(plan.Legs, stay(len(plan.Legs)+1, from, &id, departure, plan.Geometry[len(plan.Geometry)-1], req.Constraints.MovementModes[0], degraded, policy.Currency))
		step := restStep(id, x)
		step.Position, step.ArrivalAt = len(plan.Steps)+1, departure
		plan.Steps = append(plan.Steps, step)
		departure, from = x.EndAt, &id
	}
	// pausesAt writes the lunch pause and the rests due before visit i in the order they happen.
	pausesAt := func(i int) {
		rests := slices.DeleteFunc(slices.Clone(r.branch.Rests), func(x domain.Rest) bool { return x.At != i })
		lunchHere := lunch != nil && !lunch.Venue && lunch.At == i
		for _, x := range rests {
			if lunchHere && lunch.StartAt.Before(x.StartAt) {
				pause()
				lunchHere = false
			}
			rest(x)
		}
		if lunchHere {
			pause()
		}
	}
	unknownPrice := false
	for i, v := range visits {
		pausesAt(i)
		id := p.newID()
		c := v.Candidate
		in.Candidates[id] = *c
		obligation := obligationFor(req.Constraints.Obligations, c)
		plan.Legs = append(plan.Legs, leg(len(plan.Legs)+1, from, &id, departure, v.ArrivalAt, v.Transit, plan.Geometry[len(plan.Geometry)-1], c.Place.Location, policy.Currency))
		step := domain.Step{
			VisitID: id, Kind: domain.StepVisit, Position: len(plan.Steps) + 1,
			ArrivalAt: v.ArrivalAt, VisitStartAt: v.StartAt, VisitEndAt: v.EndAt, DepartureAt: v.EndAt,
			MinDuration:        c.Window.MinDuration,
			Obligation:         obligation != nil,
			Participation:      participation(c, obligation),
			Catalog:            snapshot(c),
			Cost:               &costs[i],
			AppliedConstraints: softConstraints(c, r.archetype, req.Constraints.InterestMask),
		}
		if obligation != nil {
			// No transition is verified yet, so reaching a committed session is always an estimate.
			step.AppliedConstraints = append(step.AppliedConstraints, domain.AppliedConstraint{
				Code: "OBLIGATION_REACHABLE", Strength: domain.StrengthHard, Outcome: domain.OutcomeConditional,
				Message: "The session is reachable by the estimated travel time; allow extra time",
			})
			plan.Warnings = append(plan.Warnings, domain.Warning{
				Code: "UNVERIFIED_TRANSITION", Scope: domain.ScopeVisit, VisitID: &id,
				Message: "Travel to this committed session is estimated, not verified",
			})
		}
		if lunch != nil && lunch.Venue && lunch.At == i {
			step.AppliedConstraints = append(step.AppliedConstraints, domain.AppliedConstraint{
				Code: "LUNCH_WINDOW", Strength: domain.StrengthSoft, Outcome: domain.OutcomeSatisfied,
				Message: "Lunch at a place to eat near the route",
			})
		}
		plan.Steps = append(plan.Steps, step)
		if costs[i].Price.Status == domain.PriceUnknown {
			unknownPrice = true
		}
		plan.Geometry = append(plan.Geometry, c.Place.Location)
		departure, from = v.EndAt, &id
	}
	pausesAt(len(visits))
	if req.Destination != nil && r.branch.Finish != nil {
		finish := *r.branch.Finish
		plan.Legs = append(plan.Legs, leg(len(plan.Legs)+1, from, nil, departure, departure.Add(finish.Duration), finish, plan.Geometry[len(plan.Geometry)-1], *req.Destination, policy.Currency))
		plan.Geometry = append(plan.Geometry, *req.Destination)
	}
	if slices.ContainsFunc(plan.Legs, func(l domain.Leg) bool { return l.Mode != domain.MovementWalk }) {
		plan.Warnings = append(plan.Warnings, domain.Warning{
			Code: "TRANSPORT_COST_NOT_INCLUDED", Scope: domain.ScopeRoute,
			Message: "Public transport and car costs are not included in the route cost",
		})
	}
	if degraded {
		plan.Warnings = append(plan.Warnings, degradedWarning())
	}
	cons := req.Constraints
	if unknownPrice && (cons.Budget.Mode == domain.BudgetStrict || cons.PushkinCardOnly) {
		plan.Result = domain.ResultPartial
	}
	return plan, in, nil
}

const restBreak = "REST_BREAK"

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

func isRestStep(s domain.Step) bool {
	return s.Kind == domain.StepFreeTime && slices.ContainsFunc(s.AppliedConstraints, func(c domain.AppliedConstraint) bool { return c.Code == restBreak })
}

// leg joins two stops; a nil visit is the origin before the first stop or the destination after the last.
func leg(position int, from, to *domain.VisitID, departure, arrival time.Time, t domain.TransitEstimate, a, b domain.Coordinate, currency string) domain.Leg {
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
func fare(t domain.TransitEstimate, currency string) domain.CostSnapshot {
	provenance := domain.Provenance{SourceName: t.Evidence.Provider, FetchedAt: t.Evidence.ObservedAt}
	if t.Mode == domain.MovementWalk {
		zero := int64(0)
		return domain.CostSnapshot{Price: domain.Price{Status: domain.PriceFree, Currency: currency, LowerMinor: &zero, UpperMinor: &zero}, Provenance: provenance}
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
	i := slices.IndexFunc(obligations, func(o domain.Obligation) bool { return o.SessionID != nil && *o.SessionID == c.Session.ID })
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

func softConstraints(c *domain.Candidate, archetype domain.Archetype, interests domain.InterestMask) []domain.AppliedConstraint {
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
