package usecase

import (
	"context"
	"slices"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/pricing"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/solver"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/validation"
)

// entry is a stop of the recomputed route after its history.
type entry struct {
	step      domain.Step
	candidate *domain.Candidate
	// Travel into the stop; nil for a pause, where the user stays put.
	transit  *domain.TransitEstimate
	location domain.Coordinate
	// Index of the step ahead in the base plan; replacements have none.
	ahead int
	fresh bool
}

// arrange turns the repair into stops, fills freed slots with replacements and records how each
// step ahead changed.
func (w *rework) arrange(ctx context.Context, s *solver.Solver, transit solver.Transit, problem solver.Problem, repair solver.Repair, order []int, pool []domain.Candidate, newID func() domain.VisitID) ([]entry, *domain.TransitEstimate, error) {
	var entries []entry
	for _, i := range repair.Dropped {
		w.remove(w.future[order[i]].step.VisitID, "The visit no longer fits into the day")
	}
	for _, stop := range repair.Stops {
		fi := order[stop.Step]
		old := w.future[fi].step
		e := entry{step: old, ahead: fi, location: w.position}
		if n := len(entries); n > 0 {
			e.location = entries[n-1].location
		}
		if stop.Visit == nil {
			e.step.ArrivalAt, e.step.VisitStartAt, e.step.VisitEndAt, e.step.DepartureAt = stop.PauseStart, stop.PauseStart, stop.PauseEnd, stop.PauseEnd
		} else {
			v := stop.Visit
			transit := v.Transit
			e.candidate, e.transit, e.location = v.Candidate, &transit, v.Candidate.Place.Location
			e.step.ArrivalAt, e.step.VisitStartAt, e.step.VisitEndAt, e.step.DepartureAt = v.ArrivalAt, v.StartAt, v.EndAt, v.EndAt
			e.step.MinDuration = v.Candidate.Window.MinDuration
			e.step.Catalog = snapshot(v.Candidate)
		}
		if shift := e.step.VisitStartAt.Sub(old.VisitStartAt); shift != 0 {
			w.changes = append(w.changes, domain.RouteChange{
				Kind: domain.ChangeTimeShifted, Scope: domain.ScopeVisit, BeforeVisitID: &old.VisitID, AfterVisitID: &old.VisitID,
				Message: "The visit moves in time", Details: domain.TimeShift{Delta: shift},
			})
		}
		entries = append(entries, e)
	}
	finish := repair.Finish
	if _, delayed := w.req.Trigger.(domain.DelayTrigger); delayed {
		return entries, finish, nil
	}
	for g, a := range w.future {
		if !a.gap {
			continue
		}
		var err error
		if entries, finish, err = w.fill(ctx, s, transit, problem, entries, finish, g, pool, newID); err != nil {
			return nil, nil, err
		}
	}
	return entries, finish, nil
}

// fill searches the best replacements for the freed slot of step g, between its neighbours.
func (w *rework) fill(ctx context.Context, s *solver.Solver, transit solver.Transit, problem solver.Problem, entries []entry, finish *domain.TransitEstimate, g int, pool []domain.Candidate, newID func() domain.VisitID) ([]entry, *domain.TransitEstimate, error) {
	next := slices.IndexFunc(entries, func(e entry) bool { return !e.fresh && e.ahead > g })
	if next < 0 {
		next = len(entries)
	}
	gap := problem
	gap.Anchors, gap.Start, gap.Origin = nil, w.start, w.position
	if next > 0 {
		gap.Start, gap.Origin = entries[next-1].step.DepartureAt, entries[next-1].location
	}
	if next < len(entries) {
		// The next stop waits for its own start anyway, so the whole time until then is free.
		gap.Destination, gap.End = nil, entries[next].step.VisitStartAt
		if c := entries[next].candidate; c != nil {
			gap.Destination, gap.End = &entries[next].location, arrivalDeadline(entries[next])
		}
	}
	if !gap.End.After(gap.Start) {
		return entries, finish, nil
	}
	// A replacement never brings back what the user or the organiser just took out.
	gap.Visited = append(append(w.historyPlaces(), placesOf(entries)...), w.removedPlaces()...)
	gap.Pricing = w.leftover(pricing.PolicyFor(problem.Pricing.Currency, w.req.Constraints), entries)
	free := slices.DeleteFunc(slices.Clone(pool), func(c domain.Candidate) bool { return slices.Contains(gap.Visited, c.Place.ID) })
	routes, err := s.Search(ctx, gap, free)
	if err != nil || len(routes) == 0 {
		return entries, finish, err
	}
	best := routes[0]
	updated := slices.Clone(entries)
	here, departure := best.Position, best.Now
	reconnected := false
	for i := next; i < len(updated); i++ {
		e := &updated[i]
		if e.candidate == nil {
			if departure.After(e.step.VisitStartAt) {
				return entries, finish, nil
			}
			e.location = here
			here, departure = e.location, e.step.DepartureAt
			continue
		}
		leg, ok := transit.Estimate(here, e.location, departure, problem.Modes)
		if !ok || departure.Add(leg.Duration).After(arrivalDeadline(*e)) {
			return entries, finish, nil
		}
		e.transit = &leg
		e.step.ArrivalAt = departure.Add(leg.Duration)
		reconnected = true
		break
	}
	if !reconnected && problem.Destination != nil {
		leg, ok := transit.Estimate(here, *problem.Destination, departure, problem.Modes)
		if !ok || departure.Add(leg.Duration).After(problem.End) {
			return entries, finish, nil
		}
		finish = &leg
	}
	entries = updated

	removed := w.future[g].step.VisitID
	w.changes = slices.DeleteFunc(w.changes, func(c domain.RouteChange) bool {
		return c.Kind == domain.ChangeRemoved && c.BeforeVisitID != nil && *c.BeforeVisitID == removed
	})
	var fillers []entry
	for _, v := range best.Visits {
		id := newID()
		transit := v.Transit
		c := v.Candidate
		fillers = append(fillers, entry{
			step: domain.Step{
				VisitID: id, Kind: domain.StepVisit, ArrivalAt: v.ArrivalAt, VisitStartAt: v.StartAt, VisitEndAt: v.EndAt, DepartureAt: v.EndAt,
				MinDuration: c.Window.MinDuration, Participation: participation(c, nil), Catalog: snapshot(c),
				AppliedConstraints: softConstraints(c, w.req.Base.Archetype, w.req.Constraints.InterestMask),
			},
			candidate: c, transit: &transit, location: c.Place.Location, ahead: -1, fresh: true,
		})
		w.changes = append(w.changes, domain.RouteChange{
			Kind: domain.ChangeReplaced, Scope: domain.ScopeVisit, BeforeVisitID: &removed, AfterVisitID: &id,
			Message: "A new visit takes the freed time",
		})
	}
	return slices.Insert(entries, next, fillers...), finish, nil
}

func arrivalDeadline(e entry) time.Time {
	w := e.candidate.Window
	if w.Kind == domain.WindowFixed && w.LateEntryAllowed != nil && *w.LateEntryAllowed && e.step.VisitStartAt.After(w.Start) {
		return e.step.VisitStartAt
	}
	return e.step.VisitStartAt.Add(-w.ArrivalBuffer)
}

func placesOf(entries []entry) []domain.PlaceID {
	var places []domain.PlaceID
	for _, e := range entries {
		if e.candidate != nil {
			places = append(places, e.candidate.Place.ID)
		}
	}
	return places
}

func (w *rework) historyOverBudget(policy pricing.Policy) bool {
	if policy.Budget.Mode != domain.BudgetStrict {
		return false
	}
	var spent int64
	for _, s := range w.history {
		if s.Cost == nil {
			continue
		}
		if top, known := s.Cost.Price.UpperBound(); known {
			spent += top.AmountMinor
			if spent > policy.Budget.Limit.AmountMinor {
				return true
			}
		}
	}
	return false
}

// leftover is the policy with a strict budget reduced by what the rest of the route already costs.
func (w *rework) leftover(policy pricing.Policy, entries []entry) pricing.Policy {
	if policy.Budget.Mode != domain.BudgetStrict {
		return policy
	}
	limit := *policy.Budget.Limit
	for _, s := range w.history {
		if s.Cost == nil {
			continue
		}
		if top, known := s.Cost.Price.UpperBound(); known {
			limit.AmountMinor = max(0, limit.AmountMinor-top.AmountMinor)
		}
	}
	for _, e := range entries {
		if e.candidate == nil {
			continue
		}
		if q, ok := policy.Quote(e.candidate); ok {
			if top, known := q.Price.UpperBound(); known {
				limit.AmountMinor = max(0, limit.AmountMinor-top.AmountMinor)
			}
		}
	}
	policy.Budget.Limit = &limit
	return policy
}

// plan assembles the candidate: the history as it happened, then the stops ahead.
func (w *rework) plan(policy pricing.Policy, entries []entry, finish *domain.TransitEstimate, degraded bool, data domain.DataFreshness) (domain.Plan, validation.Input, error) {
	base := w.req.Base
	plan := domain.Plan{
		Archetype: base.Archetype, Start: base.Start, End: base.End, Origin: base.Origin, Destination: base.Destination,
		CatalogRevision: data.CatalogRevision, Result: domain.ResultReady, Geometry: []domain.Coordinate{base.Origin},
	}
	constraints := w.req.Constraints
	constraints.Obligations = w.obligations
	in := validation.Input{
		Constraints: constraints, Currency: policy.Currency, Degraded: degraded,
		Candidates: map[domain.VisitID]domain.Candidate{}, History: map[domain.VisitID]struct{}{},
	}
	var snapshots []domain.CostSnapshot
	if len(w.history) > 0 && w.history[0].ArrivalAt.Before(plan.Start) {
		plan.Start = w.history[0].ArrivalAt
	}
	if n := len(w.history); n > 0 && w.history[n-1].DepartureAt.After(plan.End) {
		plan.End = w.history[n-1].DepartureAt
	}
	departure, from, here := plan.Start, (*domain.VisitID)(nil), base.Origin
	for _, done := range w.history {
		step := done
		in.History[step.VisitID] = struct{}{}
		plan.Legs = append(plan.Legs, historyLeg(base, len(plan.Legs)+1, from, step, departure))
		plan.Steps = append(plan.Steps, step)
		if step.Cost != nil {
			snapshots = append(snapshots, *step.Cost)
		}
		id := step.VisitID
		departure, from = step.DepartureAt, &id
		if loc, ok := arrivedAt(base, step.VisitID); ok {
			here = loc
			plan.Geometry = append(plan.Geometry, loc)
		}
	}
	departure, here = later(w.start, departure), w.position
	var visits []*domain.Candidate
	for _, e := range entries {
		if e.candidate != nil {
			visits = append(visits, e.candidate)
		}
	}
	costs, _, err := policy.Cost(visits)
	if err != nil {
		return domain.Plan{}, validation.Input{}, err
	}
	k := 0
	for _, e := range entries {
		step := e.step
		id := step.VisitID
		if e.candidate == nil {
			plan.Legs = append(plan.Legs, stay(len(plan.Legs)+1, from, &id, departure, here, w.req.Constraints.MovementModes[0], degraded, policy.Currency))
		} else {
			plan.Legs = append(plan.Legs, leg(len(plan.Legs)+1, from, &id, departure, step.ArrivalAt, *e.transit, here, e.location, policy.Currency))
			step.Cost = &costs[k]
			snapshots = append(snapshots, costs[k])
			k++
			in.Candidates[id] = *e.candidate
			if step.Obligation {
				w.markObligation(&plan, &step)
			}
		}
		plan.Steps = append(plan.Steps, step)
		plan.Geometry = append(plan.Geometry, e.location)
		departure, from, here = step.DepartureAt, &id, e.location
	}
	if base.Destination != nil && finish != nil {
		plan.Legs = append(plan.Legs, leg(len(plan.Legs)+1, from, nil, departure, departure.Add(finish.Duration), *finish, here, *base.Destination, policy.Currency))
		plan.Geometry = append(plan.Geometry, *base.Destination)
	}
	for i := range plan.Steps {
		plan.Steps[i].Position = i + 1
	}
	summary, err := policy.Summarize(snapshots)
	if err != nil {
		return domain.Plan{}, validation.Input{}, err
	}
	plan.Cost = summary
	if slices.ContainsFunc(plan.Legs, func(l domain.Leg) bool { return l.Mode != domain.MovementWalk }) {
		plan.Warnings = append(plan.Warnings, domain.Warning{
			Code: "TRANSPORT_COST_NOT_INCLUDED", Scope: domain.ScopeRoute,
			Message: "Public transport and car costs are not included in the route cost",
		})
	}
	if degraded {
		plan.Warnings = append(plan.Warnings, degradedWarning())
	}
	unknown := slices.ContainsFunc(snapshots, func(s domain.CostSnapshot) bool { return s.Price.Status == domain.PriceUnknown })
	if unknown && (constraints.Budget.Mode == domain.BudgetStrict || constraints.PushkinCardOnly) {
		plan.Result = domain.ResultPartial
	}
	return plan, in, nil
}

// markObligation adds the conditional reachability of a commitment ahead, once.
func (w *rework) markObligation(plan *domain.Plan, step *domain.Step) {
	id := step.VisitID
	if !slices.ContainsFunc(step.AppliedConstraints, func(c domain.AppliedConstraint) bool { return c.Code == "OBLIGATION_REACHABLE" }) {
		step.AppliedConstraints = append(slices.Clone(step.AppliedConstraints), domain.AppliedConstraint{
			Code: "OBLIGATION_REACHABLE", Strength: domain.StrengthHard, Outcome: domain.OutcomeConditional,
			Message: "The session is reachable by the estimated travel time; allow extra time",
		})
	}
	plan.Warnings = append(plan.Warnings, domain.Warning{
		Code: "UNVERIFIED_TRANSITION", Scope: domain.ScopeVisit, VisitID: &id,
		Message: "Travel to this committed session is estimated, not verified",
	})
}

// historyLeg keeps the planned travel to a visit already done, retimed to what happened.
func historyLeg(base domain.Plan, position int, from *domain.VisitID, step domain.Step, departure time.Time) domain.Leg {
	i := slices.IndexFunc(base.Legs, func(l domain.Leg) bool { return l.ToVisitID != nil && *l.ToVisitID == step.VisitID })
	l := base.Legs[i]
	l.Position, l.FromVisitID, l.From = position, from, domain.EndpointVisit
	if from == nil {
		l.From = domain.EndpointOrigin
	}
	l.DepartureAt, l.ArrivalAt = departure, later(departure, step.ArrivalAt)
	if l.ArrivalAt.After(step.ArrivalAt) {
		l.ArrivalAt = step.ArrivalAt
	}
	return l
}

// stay is the empty travel into a pause: the user remains where the last stop was.
func stay(position int, from, to *domain.VisitID, at time.Time, here domain.Coordinate, mode domain.MovementMode, degraded bool, currency string) domain.Leg {
	status := domain.VerificationEstimated
	if degraded {
		status = domain.VerificationUnknown
	}
	zero, distance := int64(0), 0.0
	evidence := domain.LegEvidence{Provider: "optimizer", Method: "no_travel", ObservedAt: at, Mode: string(mode), Limitations: []string{"user_stays_in_place"}}
	l := domain.Leg{
		Position: position, From: domain.EndpointVisit, To: domain.EndpointVisit, FromVisitID: from, ToVisitID: to,
		DepartureAt: at, ArrivalAt: at, Mode: mode, DistanceMeters: &distance, Geometry: []domain.Coordinate{here, here},
		Verification: status, Evidence: evidence,
		Cost: domain.CostSnapshot{
			Price:      domain.Price{Status: domain.PriceFree, Currency: currency, LowerMinor: &zero, UpperMinor: &zero},
			Provenance: domain.Provenance{SourceName: evidence.Provider, FetchedAt: at},
		},
	}
	if from == nil {
		l.From = domain.EndpointOrigin
	}
	return l
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func (w *rework) removedPlaces() []domain.PlaceID {
	var places []domain.PlaceID
	for _, s := range w.req.Base.Steps {
		if e, ok := w.executions[s.VisitID]; ok && e.Status == domain.ExecutionSkipped && s.Catalog != nil {
			places = append(places, s.Catalog.PlaceID)
		}
	}
	for _, a := range w.future {
		if a.gap && a.step.Catalog != nil {
			places = append(places, a.step.Catalog.PlaceID)
		}
	}
	return places
}
