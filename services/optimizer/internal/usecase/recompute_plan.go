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
func (w *rework) arrange(
	ctx context.Context,
	s *solver.Solver,
	transit solver.Transit,
	problem *solver.Problem,
	repair solver.Repair,
	order []int,
	pool []domain.Candidate,
	newID func() domain.VisitID,
) ([]entry, *domain.TransitEstimate, error) {
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
			if stop.Transit != nil {
				e.transit = stop.Transit
				e.location = e.step.ExternalVenue.Position
				e.step.ArrivalAt = stop.ArrivalAt
			}
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
				Kind:          domain.ChangeTimeShifted,
				Scope:         domain.ScopeVisit,
				BeforeVisitID: &old.VisitID,
				AfterVisitID:  &old.VisitID,
				Message:       "The visit moves in time",
				Details:       domain.TimeShift{Delta: shift},
			})
		}
		entries = append(entries, e)
	}
	finish := repair.Finish
	if _, delayed := w.req.Trigger.(domain.DelayTrigger); delayed {
		return entries, finish, nil
	}
	for g := range w.future {
		if !w.future[g].gap {
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
func (w *rework) fill(
	ctx context.Context,
	s *solver.Solver,
	transit solver.Transit,
	problem *solver.Problem,
	entries []entry,
	finish *domain.TransitEstimate,
	g int,
	pool []domain.Candidate,
	newID func() domain.VisitID,
) ([]entry, *domain.TransitEstimate, error) {
	gap, next := w.gapProblem(problem, entries, g)
	if !gap.End.After(gap.Start) {
		return entries, finish, nil
	}
	// A replacement never brings back what the user or the organiser just took out.
	gap.Visited = append(append(w.historyPlaces(), placesOf(entries)...), w.removedPlaces()...)
	policy := pricing.PolicyFor(problem.Pricing.Currency, &w.req.Constraints)
	gap.Pricing = w.leftover(&policy, entries)
	free := slices.DeleteFunc(
		slices.Clone(pool),
		func(c domain.Candidate) bool { return slices.Contains(gap.Visited, c.Place.ID) },
	)
	routes, err := s.Search(ctx, &gap, free)
	if err != nil || len(routes) == 0 {
		return entries, finish, err
	}
	best := routes[0]
	updated, nextFinish, ok := reconnectGap(transit, problem, entries, finish, next, best.Position, best.Now)
	if !ok {
		return entries, finish, nil
	}
	fillers := w.buildGapFillers(g, gap.Origin, best, newID)
	return slices.Insert(updated, next, fillers...), nextFinish, nil
}

func (w *rework) gapProblem(problem *solver.Problem, entries []entry, g int) (gap solver.Problem, next int) {
	next = slices.IndexFunc(entries, func(e entry) bool { return !e.fresh && e.ahead > g })
	if next < 0 {
		next = len(entries)
	}
	gap = *problem
	gap.Anchors, gap.Start, gap.Origin = nil, w.start, w.position
	gap.VisitsSinceRest = visitsSinceRest(w.history, entries[:next])
	if next > 0 {
		gap.Start, gap.Origin = entries[next-1].step.DepartureAt, entries[next-1].location
	}
	if next < len(entries) {
		// The next stop waits for its own start anyway, so the whole time until then is free.
		gap.Destination, gap.End = nil, entries[next].step.VisitStartAt
		if c := entries[next].candidate; c != nil {
			gap.Destination, gap.End = &entries[next].location, arrivalDeadline(&entries[next])
		}
	}
	return gap, next
}

func reconnectGap(
	transit solver.Transit,
	problem *solver.Problem,
	entries []entry,
	finish *domain.TransitEstimate,
	next int,
	here domain.Coordinate,
	departure time.Time,
) ([]entry, *domain.TransitEstimate, bool) {
	updated := slices.Clone(entries)
	reconnected := false
	for i := next; i < len(updated); i++ {
		e := &updated[i]
		if e.candidate == nil && e.step.ExternalVenue == nil {
			if departure.After(e.step.VisitStartAt) {
				return entries, finish, false
			}
			e.location = here
			here, departure = e.location, e.step.DepartureAt
			continue
		}
		legEst, ok := transit.Estimate(here, e.location, departure, problem.Modes)
		if !ok || departure.Add(legEst.Duration).After(arrivalDeadline(e)) {
			return entries, finish, false
		}
		e.transit = &legEst
		e.step.ArrivalAt = departure.Add(legEst.Duration)
		reconnected = true
		break
	}
	if !reconnected && problem.Destination != nil {
		legEst, ok := transit.Estimate(here, *problem.Destination, departure, problem.Modes)
		if !ok || departure.Add(legEst.Duration).After(problem.End) {
			return entries, finish, false
		}
		finish = &legEst
	}
	return updated, finish, true
}

func (w *rework) buildGapFillers(
	g int,
	origin domain.Coordinate,
	best *domain.Branch,
	newID func() domain.VisitID,
) []entry {
	removed := w.future[g].step.VisitID
	w.changes = slices.DeleteFunc(w.changes, func(c domain.RouteChange) bool {
		return c.Kind == domain.ChangeRemoved && c.BeforeVisitID != nil && *c.BeforeVisitID == removed
	})
	var fillers []entry
	position := origin
	restsBefore := func(j int) {
		for _, x := range best.Rests {
			if x.At == j {
				fillers = append(fillers, entry{step: restStep(newID(), x), location: position, ahead: -1, fresh: true})
			}
		}
	}
	for j := range best.Visits {
		v := &best.Visits[j]
		restsBefore(j)
		position = v.Candidate.Place.Location
		id := newID()
		transit := v.Transit
		c := v.Candidate
		fillers = append(fillers, entry{
			step: domain.Step{
				VisitID:            id,
				Kind:               domain.StepVisit,
				ArrivalAt:          v.ArrivalAt,
				VisitStartAt:       v.StartAt,
				VisitEndAt:         v.EndAt,
				DepartureAt:        v.EndAt,
				MinDuration:        c.Window.MinDuration,
				Participation:      participation(c, nil),
				Catalog:            snapshot(c),
				AppliedConstraints: softConstraints(c, w.req.Base.Archetype, w.req.Constraints.InterestMask),
			},
			candidate: c, transit: &transit, location: c.Place.Location, ahead: -1, fresh: true,
		})
		w.changes = append(w.changes, domain.RouteChange{
			Kind: domain.ChangeReplaced, Scope: domain.ScopeVisit, BeforeVisitID: &removed, AfterVisitID: &id,
			Message: "A new visit takes the freed time",
		})
	}
	restsBefore(len(best.Visits))
	return fillers
}

func arrivalDeadline(e *entry) time.Time {
	if e.candidate == nil {
		return e.step.VisitStartAt
	}
	w := e.candidate.Window
	if w.Kind == domain.WindowFixed && w.LateEntryAllowed != nil && *w.LateEntryAllowed &&
		e.step.VisitStartAt.After(w.Start) {
		return e.step.VisitStartAt
	}
	return e.step.VisitStartAt.Add(-w.ArrivalBuffer)
}

func placesOf(entries []entry) []domain.PlaceID {
	var places []domain.PlaceID
	for i := range entries {
		if entries[i].candidate != nil {
			places = append(places, entries[i].candidate.Place.ID)
		}
	}
	return places
}

func (w *rework) historyOverBudget(policy *pricing.Policy) bool {
	if policy.Budget.Mode != domain.BudgetStrict {
		return false
	}
	var spent int64
	for i := range w.history {
		s := &w.history[i]
		if s.Cost == nil || isLunch(s) {
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
func (w *rework) leftover(policy *pricing.Policy, entries []entry) pricing.Policy {
	out := *policy
	if out.Budget.Mode != domain.BudgetStrict {
		return out
	}
	limit := *out.Budget.Limit
	for i := range w.history {
		s := &w.history[i]
		if s.Cost == nil || isLunch(s) {
			continue
		}
		if top, known := s.Cost.Price.UpperBound(); known {
			limit.AmountMinor = max(0, limit.AmountMinor-top.AmountMinor)
		}
	}
	for i := range entries {
		e := &entries[i]
		if e.candidate == nil || isLunch(&e.step) {
			continue
		}
		if q, ok := out.Quote(e.candidate); ok {
			if top, known := q.Price.UpperBound(); known {
				limit.AmountMinor = max(0, limit.AmountMinor-top.AmountMinor)
			}
		}
	}
	out.Budget.Limit = &limit
	return out
}

// plan assembles the candidate: the history as it happened, then the stops ahead.
func (w *rework) plan(
	policy *pricing.Policy,
	entries []entry,
	finish *domain.TransitEstimate,
	degraded bool,
	data domain.DataFreshness,
) (domain.Plan, validation.Input, error) {
	base := &w.req.Base
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
	departure, from, snapshots := w.appendHistoryToPlan(&plan, &in)
	departure = later(w.start, departure)
	var costs []domain.CostSnapshot
	for i := range entries {
		if entries[i].candidate != nil {
			p := *policy
			if isLunch(&entries[i].step) {
				p.Budget = domain.Budget{Mode: domain.BudgetNone}
				p.PushkinCardOnly = false
				p.AcceptUnknownPrice = true
				p.Programs = nil
			}
			priced, _, err := p.Cost([]*domain.Candidate{entries[i].candidate})
			if err != nil {
				return domain.Plan{}, validation.Input{}, err
			}
			costs = append(costs, priced[0])
		}
	}
	departure, from, here, entrySnaps := w.appendEntriesToPlan(
		&plan,
		&in,
		policy,
		entries,
		costs,
		departure,
		from,
		degraded,
	)
	snapshots = append(snapshots, entrySnaps...)
	if base.Destination != nil && finish != nil {
		plan.Legs = append(
			plan.Legs,
			leg(
				len(plan.Legs)+1,
				from,
				nil,
				departure,
				departure.Add(finish.Duration),
				finish,
				here,
				*base.Destination,
				policy.Currency,
			),
		)
		plan.Geometry = append(plan.Geometry, *base.Destination)
	}
	for i := range plan.Steps {
		plan.Steps[i].Position = i + 1
	}
	// The route budget covers tickets; lunch prices stay on their visits.
	ticketSnapshots := make([]domain.CostSnapshot, 0, len(snapshots))
	for i := range plan.Steps {
		step := &plan.Steps[i]
		meal := step.Lunch != nil || slices.ContainsFunc(step.AppliedConstraints, func(c domain.AppliedConstraint) bool {
			return c.Code == lunchWindowCode
		})
		if step.Kind == domain.StepVisit && step.Cost != nil && !meal {
			ticketSnapshots = append(ticketSnapshots, *step.Cost)
		}
	}
	summary, err := policy.Summarize(ticketSnapshots)
	if err != nil {
		return domain.Plan{}, validation.Input{}, err
	}
	plan.Cost = summary
	w.finalizeRecomputedPlan(&plan, &constraints, ticketSnapshots, degraded)
	for i := range plan.Steps {
		s := &plan.Steps[i]
		if s.ExternalVenue != nil {
			id := s.VisitID
			if s.ExternalVenue.Price.Status == domain.PriceUnknown {
				plan.Warnings = append(plan.Warnings, domain.Warning{Code: "LUNCH_PRICE_UNKNOWN", Scope: domain.ScopeVisit, VisitID: &id, Message: "The external lunch price is unknown"})
			}
			plan.Warnings = append(plan.Warnings, domain.Warning{Code: "LUNCH_AVAILABILITY_UNKNOWN", Scope: domain.ScopeVisit, VisitID: &id, Message: "The external venue's opening hours and availability are unverified"})
		}
	}
	return plan, in, nil
}

func (w *rework) appendHistoryToPlan(
	plan *domain.Plan,
	in *validation.Input,
) (departure time.Time, from *domain.VisitID, snapshots []domain.CostSnapshot) {
	base := &w.req.Base
	if len(w.history) > 0 && w.history[0].ArrivalAt.Before(plan.Start) {
		plan.Start = w.history[0].ArrivalAt
	}
	if n := len(w.history); n > 0 && w.history[n-1].DepartureAt.After(plan.End) {
		plan.End = w.history[n-1].DepartureAt
	}
	departure = plan.Start
	for i := range w.history {
		step := w.history[i]
		in.History[step.VisitID] = struct{}{}
		plan.Legs = append(plan.Legs, historyLeg(base, len(plan.Legs)+1, from, &step, departure))
		plan.Steps = append(plan.Steps, step)
		if step.Cost != nil {
			snapshots = append(snapshots, *step.Cost)
		}
		id := step.VisitID
		departure, from = step.DepartureAt, &id
		if loc, ok := arrivedAt(base, step.VisitID); ok {
			plan.Geometry = append(plan.Geometry, loc)
		}
	}
	return departure, from, snapshots
}

func (w *rework) appendEntriesToPlan(
	plan *domain.Plan,
	in *validation.Input,
	policy *pricing.Policy,
	entries []entry,
	costs []domain.CostSnapshot,
	departure time.Time,
	from *domain.VisitID,
	degraded bool,
) (nextDep time.Time, nextFrom *domain.VisitID, here domain.Coordinate, snapshots []domain.CostSnapshot) {
	here = w.position
	k := 0
	for i := range entries {
		e := &entries[i]
		step := e.step
		id := step.VisitID
		if e.candidate == nil {
			plan.Legs = append(
				plan.Legs,
				stay(
					len(plan.Legs)+1,
					from,
					&id,
					departure,
					here,
					w.req.Constraints.MovementModes[0],
					degraded,
					policy.Currency,
				),
			)
		} else {
			plan.Legs = append(
				plan.Legs,
				leg(
					len(plan.Legs)+1,
					from,
					&id,
					departure,
					step.ArrivalAt,
					e.transit,
					here,
					e.location,
					policy.Currency,
				),
			)
			if e.candidate != nil {
				step.Cost = &costs[k]
				snapshots = append(snapshots, costs[k])
				k++
				in.Candidates[id] = *e.candidate
				if step.Obligation {
					w.markObligation(plan, &step)
				}
			}
		}
		plan.Steps = append(plan.Steps, step)
		plan.Geometry = append(plan.Geometry, e.location)
		departure, from, here = step.DepartureAt, &id, e.location
	}
	return departure, from, here, snapshots
}

func (w *rework) finalizeRecomputedPlan(
	plan *domain.Plan,
	constraints *domain.RouteConstraints,
	snapshots []domain.CostSnapshot,
	degraded bool,
) {
	if slices.ContainsFunc(plan.Legs, func(l domain.Leg) bool { return l.Mode != domain.MovementWalk }) {
		plan.Warnings = append(plan.Warnings, domain.Warning{
			Code: "TRANSPORT_COST_NOT_INCLUDED", Scope: domain.ScopeRoute,
			Message: "Public transport and car costs are not included in the route cost",
		})
	}
	if degraded {
		plan.Warnings = append(plan.Warnings, degradedWarning())
	}
	unknown := slices.ContainsFunc(
		snapshots,
		func(s domain.CostSnapshot) bool { return s.Price.Status == domain.PriceUnknown },
	)
	if unknown && (constraints.Budget.Mode == domain.BudgetStrict || constraints.PushkinCardOnly) {
		plan.Result = domain.ResultPartial
	}
}

// markObligation adds the conditional reachability of a commitment ahead, once.
func (w *rework) markObligation(plan *domain.Plan, step *domain.Step) {
	id := step.VisitID
	if !slices.ContainsFunc(
		step.AppliedConstraints,
		func(c domain.AppliedConstraint) bool { return c.Code == obligationReachable },
	) {
		step.AppliedConstraints = append(slices.Clone(step.AppliedConstraints), domain.AppliedConstraint{
			Code: obligationReachable, Strength: domain.StrengthHard, Outcome: domain.OutcomeConditional,
			Message: "The session is reachable by the estimated travel time; allow extra time",
		})
	}
	plan.Warnings = append(plan.Warnings, domain.Warning{
		Code: "UNVERIFIED_TRANSITION", Scope: domain.ScopeVisit, VisitID: &id,
		Message: "Travel to this committed session is estimated, not verified",
	})
}

// historyLeg keeps the planned travel to a visit already done, retimed to what happened.
func historyLeg(
	base *domain.Plan,
	position int,
	from *domain.VisitID,
	step *domain.Step,
	departure time.Time,
) domain.Leg {
	i := slices.IndexFunc(
		base.Legs,
		func(l domain.Leg) bool { return l.ToVisitID != nil && *l.ToVisitID == step.VisitID },
	)
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
func stay(
	position int,
	from, to *domain.VisitID,
	at time.Time,
	here domain.Coordinate,
	mode domain.MovementMode,
	degraded bool,
	currency string,
) domain.Leg {
	status := domain.VerificationEstimated
	if degraded {
		status = domain.VerificationUnknown
	}
	zero, distance := int64(0), 0.0
	evidence := domain.LegEvidence{
		Provider:    "optimizer",
		Method:      "no_travel",
		ObservedAt:  at,
		Mode:        string(mode),
		Limitations: []string{"user_stays_in_place"},
	}
	l := domain.Leg{
		Position:       position,
		From:           domain.EndpointVisit,
		To:             domain.EndpointVisit,
		FromVisitID:    from,
		ToVisitID:      to,
		DepartureAt:    at,
		ArrivalAt:      at,
		Mode:           mode,
		DistanceMeters: &distance,
		Geometry:       []domain.Coordinate{here, here},
		Verification:   status,
		Evidence:       evidence,
		Cost: domain.CostSnapshot{
			Price: domain.Price{
				Status:     domain.PriceFree,
				Currency:   currency,
				LowerMinor: &zero,
				UpperMinor: &zero,
			},
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
	for i := range w.req.Base.Steps {
		s := &w.req.Base.Steps[i]
		if e, ok := w.executions[s.VisitID]; ok && e.Status == domain.ExecutionSkipped && s.Catalog != nil {
			places = append(places, s.Catalog.PlaceID)
		}
	}
	for i := range w.future {
		a := &w.future[i]
		if a.gap && a.step.Catalog != nil {
			places = append(places, a.step.Catalog.PlaceID)
		}
	}
	return places
}

// visitsSinceRest counts the visits of the day after its last rest; a lunch pause is not a rest.
func visitsSinceRest(history []domain.Step, entries []entry) int {
	steps := slices.Clone(history)
	for i := range entries {
		steps = append(steps, entries[i].step)
	}
	n := 0
	for i := len(steps) - 1; i >= 0; i-- {
		s := &steps[i]
		if isRestStep(s) {
			break
		}
		if s.Kind == domain.StepVisit {
			n++
		}
	}
	return n
}
