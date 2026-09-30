package usecase

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"go.uber.org/zap"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/pricing"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/solver"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/validation"
)

// Recompute proposes how the rest of a plan changes after a delay, a cancellation, a removal or a
// new pin. What already happened stays as it was, and a commitment is never dropped without saying so.
func (p *Planner) Recompute(ctx context.Context, req *domain.RecomputeRequest) (domain.RecomputeResult, error) {
	started := time.Now()
	policy, w, err := p.prepareRecompute(req)
	if err != nil {
		return domain.RecomputeResult{}, err
	}
	catalog, data, err := p.fetchRecomputeCatalog(ctx, req)
	if err != nil {
		return domain.RecomputeResult{}, err
	}
	done := func(res domain.RecomputeResult) (domain.RecomputeResult, error) {
		res.Data = data
		res.ComputationTime = time.Since(started)
		return res, nil
	}
	anchors, conflictRes, err := w.prepareAnchors(catalog, &policy, p.newID)
	if err != nil {
		return domain.RecomputeResult{}, err
	}
	if conflictRes != nil {
		return done(*conflictRes)
	}
	return p.executeRecompute(ctx, req, w, &policy, catalog, anchors, data, done)
}

func (p *Planner) prepareRecompute(req *domain.RecomputeRequest) (pricing.Policy, *rework, error) {
	if req.Base.Cost.KnownPersonal.Currency != p.cfg.Currency {
		return pricing.Policy{}, nil, fmt.Errorf(
			"%w: base plan currency does not match planner currency",
			ErrInvalidRequest,
		)
	}
	policy := pricing.PolicyFor(p.cfg.Currency, &req.Constraints)
	if err := policy.Validate(); err != nil {
		return pricing.Policy{}, nil, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	w, err := newRework(req)
	if err != nil {
		return pricing.Policy{}, nil, err
	}
	return policy, w, nil
}

func (p *Planner) fetchRecomputeCatalog(
	ctx context.Context,
	req *domain.RecomputeRequest,
) ([]domain.Candidate, domain.DataFreshness, error) {
	catalogReq := domain.OptimizeRequest{
		City: req.City, Timezone: req.Timezone, Start: req.Base.Start, End: req.Base.End,
		Origin: req.Base.Origin, Destination: req.Base.Destination, Constraints: req.Constraints,
	}
	catalog, data, err := p.source.Candidates(ctx, &catalogReq)
	if err != nil {
		return nil, domain.DataFreshness{}, err
	}
	if c, ok := req.Trigger.(domain.CancellationTrigger); ok && data.CatalogRevision < c.MinCatalogRevision {
		// The caller may have heard of the publication before the source did; one more read settles it.
		hint, ok := p.source.(RevisionHint)
		if !ok {
			return nil, domain.DataFreshness{}, ErrStaleCatalog
		}
		hint.Observe(req.City, c.MinCatalogRevision)
		if catalog, data, err = p.source.Candidates(ctx, &catalogReq); err != nil {
			return nil, domain.DataFreshness{}, err
		}
		if data.CatalogRevision < c.MinCatalogRevision {
			return nil, domain.DataFreshness{}, ErrStaleCatalog
		}
	}
	return catalog, data, nil
}

// rework is one recompute: what already happened, what is still ahead, and what changed.
type rework struct {
	req        domain.RecomputeRequest
	executions map[domain.VisitID]domain.VisitExecution
	history    []domain.Step
	future     []ahead
	start      time.Time
	position   domain.Coordinate
	located    bool
	// Obligations of this recompute: the request's and those marked on the plan.
	obligations []domain.Obligation
	changes     []domain.RouteChange
	conflicts   []domain.Conflict
}

// ahead is a step still to come.
type ahead struct {
	step      domain.Step
	candidate *domain.Candidate
	// The step's slot is freed and may be filled with replacements.
	gap bool
}

func (w *rework) prepareAnchors(
	catalog []domain.Candidate,
	policy *pricing.Policy,
	newID func() domain.VisitID,
) ([]solver.Anchor, *domain.RecomputeResult, error) {
	if locErr := w.locate(catalog); locErr != nil {
		return nil, nil, locErr
	}
	w.applyTrigger(newID)
	w.resolve(catalog)
	anchors := w.anchors(catalog)
	if len(w.conflicts) > 0 {
		return nil, &domain.RecomputeResult{Status: domain.RecomputeConflict, Conflicts: w.conflicts}, nil
	}
	if w.historyOverBudget(policy) {
		return nil, &domain.RecomputeResult{Status: domain.RecomputeConflict, Conflicts: []domain.Conflict{{
			Code:    "BUDGET_EXCEEDED",
			Message: "Completed visits already exceed the strict budget",
		}}}, nil
	}
	return anchors, nil, nil
}

func (p *Planner) executeRecompute(
	ctx context.Context,
	req *domain.RecomputeRequest,
	w *rework,
	policy *pricing.Policy,
	catalog []domain.Candidate,
	anchors []solver.Anchor,
	data domain.DataFreshness,
	done func(domain.RecomputeResult) (domain.RecomputeResult, error),
) (domain.RecomputeResult, error) {
	transit, degraded, err := p.transit.Transit(ctx, req.City, w.points(req, catalog), req.Constraints.MovementModes)
	if err != nil {
		return domain.RecomputeResult{}, err
	}
	s, err := solver.New(
		solver.Config{BeamWidth: p.cfg.BeamWidth, Parallelism: p.cfg.Parallelism},
		solver.DefaultScoreParams(),
		transit,
		solver.WindowPlacement{},
	)
	if err != nil {
		return domain.RecomputeResult{}, err
	}
	problem := solver.Problem{
		Start: w.start, End: req.Base.End, Origin: w.position, Destination: req.Base.Destination,
		Interests: req.Constraints.InterestMask, Archetype: req.Base.Archetype, Modes: req.Constraints.MovementModes,
		Pricing: w.leftover(policy, nil), Anchors: anchors, Visited: w.historyPlaces(),
		Load: solver.ProfileFor(req.Constraints.LoadProfile),
	}
	steps, order := w.repairSteps()
	repair, err := s.Repair(ctx, &problem, steps)
	if err != nil {
		return domain.RecomputeResult{}, err
	}
	if res, conflict := w.repairConflict(repair); conflict {
		return done(res)
	}
	for _, dropped := range repair.Dropped {
		if isLunch(&w.future[order[dropped]].step) {
			return done(domain.RecomputeResult{Status: domain.RecomputeConflict, Conflicts: []domain.Conflict{{Code: "LUNCH_UNREACHABLE", Message: "The selected lunch cannot fit with travel and the remaining commitments"}}})
		}
	}
	entries, finish, err := w.arrange(
		ctx,
		s,
		transit,
		&problem,
		repair,
		order,
		admissible(catalog, &req.Constraints),
		p.newID,
	)
	if err != nil {
		return domain.RecomputeResult{}, err
	}
	if len(w.history) == 0 && len(entries) == 0 {
		return done(
			domain.RecomputeResult{
				Status: domain.RecomputeConflict,
				Conflicts: []domain.Conflict{
					{
						Code:    "NO_FEASIBLE_ROUTE",
						Message: "No visits fit in the remaining day; choose another time interval",
					},
				},
			},
		)
	}
	candidate, in, err := w.plan(policy, entries, finish, degraded, data)
	if err != nil {
		return domain.RecomputeResult{}, err
	}
	if violations := validation.Check(&candidate, &in); len(violations) > 0 {
		rejectedPlans.WithLabelValues(violations[0].Code).Inc()
		p.log.Error("recomputed plan failed validation", zap.Any("violations", violations))
		return domain.RecomputeResult{}, errors.New("recomputed plan failed validation")
	}
	w.recordChanges(&candidate)
	if len(w.changes) == 0 && !isCopyTrigger(req.Trigger) {
		return done(domain.RecomputeResult{Status: domain.RecomputeUnchanged})
	}
	return done(domain.RecomputeResult{Status: domain.RecomputeProposed, Candidate: &candidate, Changes: w.changes})
}

func (w *rework) repairConflict(repair solver.Repair) (domain.RecomputeResult, bool) {
	switch {
	case repair.Unreachable != nil:
		return domain.RecomputeResult{
			Status:    domain.RecomputeConflict,
			Conflicts: []domain.Conflict{w.unreachable(repair.Unreachable)},
		}, true
	case repair.DestinationUnreachable:
		return domain.RecomputeResult{
			Status: domain.RecomputeConflict,
			Conflicts: []domain.Conflict{
				{
					Code:    "DESTINATION_UNREACHABLE",
					Message: "The destination cannot be reached before the day ends; change the destination or the end of the day",
				},
			},
		}, true
	default:
		return domain.RecomputeResult{}, false
	}
}

func newRework(req *domain.RecomputeRequest) (*rework, error) {
	w := &rework{req: *req, executions: make(map[domain.VisitID]domain.VisitExecution, len(req.History))}
	for _, e := range req.History {
		w.executions[e.VisitID] = e
	}
	w.start, w.position, w.located = req.Base.Start, req.Base.Origin, true
	for i := range req.Base.Steps {
		step := &req.Base.Steps[i]
		e, executed := w.executions[step.VisitID]
		switch {
		case !executed:
			w.future = append(w.future, ahead{step: *step})
		case e.Status == domain.ExecutionSkipped:
			w.remove(step.VisitID, "The visit was skipped")
		default:
			done := *step
			done.MinDuration = min(done.MinDuration, e.ActualEnd.Sub(*e.ActualStart))
			done.ArrivalAt, done.VisitStartAt, done.VisitEndAt, done.DepartureAt = *e.ActualStart, *e.ActualStart, *e.ActualEnd, *e.ActualEnd
			if n := len(w.history); n > 0 && done.ArrivalAt.Before(w.history[n-1].DepartureAt) {
				return nil, fmt.Errorf("%w: visits of the history overlap", ErrInvalidRequest)
			}
			w.history = append(w.history, done)
			w.start = done.DepartureAt
			if pos, ok := arrivedAt(&req.Base, done.VisitID); ok {
				w.position, w.located = pos, true
			} else if done.Kind == domain.StepVisit {
				w.located = false
			}
		}
	}
	if d, ok := req.Trigger.(domain.DelayTrigger); ok {
		// The caller already resolved the delay into a time; adding anything here would count it twice.
		w.start, w.position, w.located = later(d.EffectiveStart, w.start), d.Position, true
	}
	if c, ok := req.Trigger.(domain.CopyTrigger); ok {
		w.position, w.located = c.Origin, true
	}
	return w, nil
}

// arrivedAt is where the plan's travel to the visit ends; a plan without that geometry leaves it to the catalog.
func arrivedAt(plan *domain.Plan, id domain.VisitID) (domain.Coordinate, bool) {
	i := slices.IndexFunc(
		plan.Legs,
		func(l domain.Leg) bool { return l.ToVisitID != nil && *l.ToVisitID == id && len(l.Geometry) > 0 },
	)
	if i < 0 {
		return domain.Coordinate{}, false
	}
	g := plan.Legs[i].Geometry
	return g[len(g)-1], true
}

// locate finds where the last visit of the history is when the plan's geometry did not tell.
func (w *rework) locate(catalog []domain.Candidate) error {
	if w.located {
		return nil
	}
	for i := len(w.history) - 1; i >= 0; i-- {
		v := &w.history[i]
		if v.Catalog == nil {
			continue
		}
		j := slices.IndexFunc(catalog, func(c domain.Candidate) bool { return c.Place.ID == v.Catalog.PlaceID })
		if j < 0 {
			return fmt.Errorf("%w: the place of the last visit is unknown", ErrInvalidRequest)
		}
		w.position, w.located = catalog[j].Place.Location, true
		return nil
	}
	w.position, w.located = w.req.Base.Origin, true
	return nil
}

func (w *rework) remove(id domain.VisitID, message string) {
	w.changes = slices.DeleteFunc(w.changes, func(c domain.RouteChange) bool {
		return c.Kind == domain.ChangeKept && c.BeforeVisitID != nil && *c.BeforeVisitID == id
	})
	w.changes = append(
		w.changes,
		domain.RouteChange{Kind: domain.ChangeRemoved, Scope: domain.ScopeVisit, BeforeVisitID: &id, Message: message},
	)
}

func (w *rework) index(id domain.VisitID) int {
	return slices.IndexFunc(w.future, func(a ahead) bool { return a.step.VisitID == id })
}

func (w *rework) applyTrigger(newID func() domain.VisitID) {
	switch t := w.req.Trigger.(type) {
	case domain.CancellationTrigger:
		for _, id := range t.VisitIDs {
			if i := w.index(id); i >= 0 {
				w.cancel(i)
			}
		}
	case domain.RemovalTrigger:
		w.applyRemovalTrigger(t, newID)
	case domain.PinTrigger:
		w.applyPinTrigger(t)
	case domain.LunchTrigger:
		w.applyLunchTrigger(t)
	}
}

func (w *rework) applyLunchTrigger(t domain.LunchTrigger) {
	original := map[domain.VisitID]domain.Step{}
	for _, a := range w.future {
		original[a.step.VisitID] = a.step
	}
	w.future = slices.DeleteFunc(w.future, func(a ahead) bool {
		return a.step.Lunch != nil || (isLunch(&a.step) && slices.ContainsFunc(t.Stops, func(s domain.LunchStop) bool { return s.VisitID == a.step.VisitID }))
	})
	for _, stop := range t.Stops {
		if slices.ContainsFunc(w.history, func(s domain.Step) bool { return s.VisitID == stop.VisitID }) {
			continue
		}
		step, found := original[stop.VisitID]
		if !found {
			step = domain.Step{VisitID: stop.VisitID, Participation: domain.Participation{Status: domain.ParticipationNotRequired, Evidence: domain.EvidenceNone}}
		}
		step.Lunch = &domain.LunchMetadata{AfterVisitID: stop.AfterVisitID, Duration: stop.Duration}
		step.MinDuration = stop.Duration
		step.Pinned = false
		step.Obligation = false
		step.ExternalVenue = nil
		step.Catalog = nil
		step.Cost = nil
		switch {
		case stop.External != nil:
			step.Kind = domain.StepExternalLunch
			venue := *stop.External
			step.ExternalVenue = &venue
		case stop.CatalogVisitID != nil:
			if old, ok := original[*stop.CatalogVisitID]; ok {
				step = old
				step.Lunch = &domain.LunchMetadata{AfterVisitID: stop.AfterVisitID, Duration: stop.Duration}
				step.MinDuration = stop.Duration
			}
		default:
			step.Kind = domain.StepFreeTime
		}
		step.AppliedConstraints = slices.DeleteFunc(slices.Clone(step.AppliedConstraints), func(c domain.AppliedConstraint) bool { return c.Code == lunchWindowCode })
		anchor := slices.IndexFunc(w.future, func(a ahead) bool { return a.step.VisitID == stop.AfterVisitID })
		insert := anchor + 1
		for insert < len(w.future) && w.future[insert].step.Lunch != nil && w.future[insert].step.Lunch.AfterVisitID == stop.AfterVisitID {
			insert++
		}
		if anchor < 0 && !slices.ContainsFunc(w.history, func(s domain.Step) bool { return s.VisitID == stop.AfterVisitID }) {
			w.conflicts = append(w.conflicts, domain.Conflict{Code: "LUNCH_ANCHOR_MISSING", Message: "The lunch anchor is no longer in the route"})
			continue
		}
		w.future = slices.Insert(w.future, insert, ahead{step: step})
		if !found {
			id := step.VisitID
			w.changes = append(w.changes, domain.RouteChange{Kind: domain.ChangeAdded, Scope: domain.ScopeVisit, AfterVisitID: &id, Message: "A lunch was added after its selected stop"})
		}
	}
	for id, old := range original {
		if old.Lunch != nil && !slices.ContainsFunc(t.Stops, func(s domain.LunchStop) bool { return s.VisitID == id }) {
			w.remove(id, "The lunch was removed at the user's request")
		}
	}
}

func (w *rework) applyRemovalTrigger(t domain.RemovalTrigger, newID func() domain.VisitID) {
	i := w.index(t.VisitID)
	if i < 0 {
		return
	}
	old := w.future[i].step
	if t.Mode == domain.RemovalRebuild {
		w.future[i].gap = true
		w.remove(old.VisitID, "The visit was removed at the user's request")
		return
	}
	pause := domain.Step{
		VisitID:       newID(),
		Kind:          domain.StepFreeTime,
		ArrivalAt:     old.VisitStartAt,
		VisitStartAt:  old.VisitStartAt,
		VisitEndAt:    old.VisitEndAt,
		DepartureAt:   old.VisitEndAt,
		Participation: domain.Participation{Status: domain.ParticipationNotRequired, Evidence: domain.EvidenceNone},
	}
	w.future[i] = ahead{step: pause}
	w.changes = append(w.changes, domain.RouteChange{
		Kind:          domain.ChangeReplaced,
		Scope:         domain.ScopeVisit,
		BeforeVisitID: &old.VisitID,
		AfterVisitID:  &pause.VisitID,
		Message:       "The visit was removed and its time left free",
	})
}

func (w *rework) applyPinTrigger(t domain.PinTrigger) {
	i := w.index(t.VisitID)
	if i < 0 || w.future[i].step.Kind != domain.StepVisit {
		return
	}
	step := &w.future[i].step
	step.Pinned = t.Kind != domain.PinNone
	step.Obligation = t.Kind == domain.PinObligation || w.committed(step)
	if !step.Obligation {
		step.AppliedConstraints = slices.DeleteFunc(
			slices.Clone(step.AppliedConstraints),
			func(c domain.AppliedConstraint) bool {
				return c.Code == obligationReachable
			},
		)
	}
	w.changes = append(w.changes, domain.RouteChange{
		Kind:          domain.ChangeKept,
		Scope:         domain.ScopeVisit,
		BeforeVisitID: &step.VisitID,
		AfterVisitID:  &step.VisitID,
		Message:       pinMessage(t.Kind),
	})
}

func pinMessage(kind domain.PinKind) string {
	switch kind {
	case domain.PinObligation:
		return "The visit is now a commitment and stays in every recompute"
	case domain.PinPreferred:
		return "The visit is preferred; it may be replaced with an explanation"
	case domain.PinNone:
		return "The visit is no longer pinned"
	default:
		return "The visit is no longer pinned"
	}
}

// committed tells whether the request's own obligations hold the step's session.
func obligationMatches(o domain.Obligation, step *domain.Step) bool {
	return (o.VisitID != nil && *o.VisitID == step.VisitID) ||
		(o.SessionID != nil && step.Catalog != nil && step.Catalog.SessionID != nil && *o.SessionID == *step.Catalog.SessionID)
}

func (w *rework) committed(step *domain.Step) bool {
	return slices.ContainsFunc(
		w.req.Constraints.Obligations,
		func(o domain.Obligation) bool { return obligationMatches(o, step) },
	)
}

// cancel takes the step out; a cancelled commitment is removed only by an explicit change the user
// has to accept, together with what to do about the ticket.
func (w *rework) cancel(i int) {
	step := w.future[i].step
	w.future[i].gap = true
	if step.Obligation || w.committed(&step) {
		w.remove(step.VisitID, "The committed session was cancelled by its organiser")
		w.changes = append(w.changes, domain.RouteChange{
			Kind:          domain.ChangeParticipationAction,
			Scope:         domain.ScopeVisit,
			BeforeVisitID: &step.VisitID,
			Message:       "Ask the organiser for a refund or another session",
			Details:       domain.ParticipationAction{Action: "refund_or_rebook"},
		})
		return
	}
	w.remove(step.VisitID, "The visit was cancelled by its organiser")
}

// resolve finds each visit ahead in the catalog, whose windows and prices are current.
func (w *rework) resolve(catalog []domain.Candidate) {
	for i := range w.future {
		a := &w.future[i]
		if a.gap || a.step.Kind != domain.StepVisit {
			continue
		}
		j := slices.IndexFunc(catalog, func(c domain.Candidate) bool { return sameVisitAs(&c, a.step.Catalog) })
		switch {
		case j < 0 && (a.step.Obligation || w.committed(&a.step)):
			w.conflicts = append(w.conflicts, visitConflict("OBLIGATION_UNAVAILABLE",
				"The committed session is no longer in the catalog; check it with the organiser", &a.step))
		case j < 0:
			a.gap = true
			w.remove(a.step.VisitID, "The visit is no longer in the catalog")
		case catalog[j].Window.HoursUnknown && !isLunch(&a.step):
			a.gap = true
			w.remove(a.step.VisitID, "The place no longer states its opening hours")
		case catalog[j].Session != nil && catalog[j].Session.Availability == domain.AvailabilityCancelled:
			w.cancel(i)
		case !isLunch(&a.step) && !a.step.Obligation && !w.committed(&a.step) && len(admissible(catalog[j:j+1], &w.req.Constraints)) == 0:
			a.gap = true
			w.remove(a.step.VisitID, "The visit is no longer available under the route constraints")
		default:
			c := catalog[j]
			if a.step.Lunch != nil {
				c.Window.MinDuration = a.step.Lunch.Duration
				c.Window.RecommendedDuration = a.step.Lunch.Duration
			}
			a.candidate = &c
		}
	}
}

// isLunch is true for the step that holds the route's lunch; only lunch may rest on assumed hours.
func isLunch(s *domain.Step) bool {
	if s.Lunch != nil {
		return true
	}
	return slices.ContainsFunc(
		s.AppliedConstraints,
		func(c domain.AppliedConstraint) bool { return c.Code == lunchWindowCode },
	)
}

func sameVisitAs(c *domain.Candidate, s *domain.CatalogSnapshot) bool {
	if c.Place.ID != s.PlaceID || (c.Session == nil) != (s.SessionID == nil) {
		return false
	}
	return c.Session == nil || c.Session.ID == *s.SessionID
}

// anchors turns every commitment still ahead into an anchor with its own window.
func (w *rework) anchors(catalog []domain.Candidate) []solver.Anchor {
	var anchors []solver.Anchor
	w.checkMissingObligations()
	for i := range w.history {
		w.addStepObligation(&w.history[i])
	}
	for i := range w.future {
		a := &w.future[i]
		if a.gap {
			continue
		}
		o, required := w.addStepObligation(&a.step)
		if !required || a.candidate == nil {
			continue
		}
		if a.candidate.Session != nil {
			found, conflicts := solver.AnchorsFor([]domain.Obligation{o}, catalog)
			anchors = append(anchors, found...)
			w.conflicts = append(w.conflicts, conflicts...)
		} else {
			c := *a.candidate
			c.Window.ArrivalBuffer = max(c.Window.ArrivalBuffer, o.ArrivalBuffer)
			anchors = append(anchors, solver.Anchor{Candidate: c, Obligation: o})
		}
	}
	return anchors
}

func (w *rework) checkMissingObligations() {
	for _, o := range w.req.Constraints.Obligations {
		if !slices.ContainsFunc(
			w.req.Base.Steps,
			func(step domain.Step) bool { return step.Kind == domain.StepVisit && obligationMatches(o, &step) },
		) {
			w.conflicts = append(
				w.conflicts,
				domain.Conflict{Code: "OBLIGATION_UNAVAILABLE", Message: "The committed visit is not in the base plan"},
			)
		}
	}
}

func (w *rework) addStepObligation(step *domain.Step) (domain.Obligation, bool) {
	if step.Kind != domain.StepVisit {
		step.Obligation = false
		return domain.Obligation{}, false
	}
	i := slices.IndexFunc(
		w.req.Constraints.Obligations,
		func(o domain.Obligation) bool { return obligationMatches(o, step) },
	)
	if i < 0 && !step.Obligation {
		return domain.Obligation{}, false
	}
	o := domain.Obligation{VisitID: &step.VisitID, Participation: step.Participation.Status}
	if step.Catalog != nil {
		o.SessionID = step.Catalog.SessionID
	}
	if i >= 0 {
		o = w.req.Constraints.Obligations[i]
		o.VisitID = &step.VisitID
		if step.Catalog != nil {
			o.SessionID = step.Catalog.SessionID
		}
		step.Participation = participation(nil, &o)
	}
	step.Obligation = true
	w.obligations = append(w.obligations, o)
	return o, true
}

func (w *rework) unreachable(a *solver.Anchor) domain.Conflict {
	c := domain.Conflict{
		Code:    "OBLIGATION_UNREACHABLE",
		Message: "The committed visit can no longer be reached in time; choose another visit or unpin it",
	}
	if a.Obligation.VisitID != nil {
		c.VisitIDs = []domain.VisitID{*a.Obligation.VisitID}
	}
	if a.Candidate.Session != nil {
		c.SessionIDs = []domain.SessionID{a.Candidate.Session.ID}
	}
	return c
}

func visitConflict(code, message string, step *domain.Step) domain.Conflict {
	c := domain.Conflict{Code: code, Message: message, VisitIDs: []domain.VisitID{step.VisitID}}
	if step.Catalog != nil && step.Catalog.SessionID != nil {
		c.SessionIDs = []domain.SessionID{*step.Catalog.SessionID}
	}
	return c
}

func (w *rework) historyPlaces() []domain.PlaceID {
	var places []domain.PlaceID
	for i := range w.history {
		s := &w.history[i]
		if s.Catalog != nil {
			places = append(places, s.Catalog.PlaceID)
		}
	}
	return places
}

// repairSteps are the steps still in the route, in order. Only a delay may move them earlier than
// planned; after any other change the plan keeps its times where it can.
func (w *rework) repairSteps() (steps []solver.RepairStep, order []int) {
	_, delayed := w.req.Trigger.(domain.DelayTrigger)
	if isCopyTrigger(w.req.Trigger) {
		delayed = true
	}
	for i := range w.future {
		a := &w.future[i]
		if a.gap {
			continue
		}
		order = append(order, i)
		step := solver.RepairStep{Candidate: a.candidate}
		step.Meal = isLunch(&a.step)
		if a.step.Kind == domain.StepFreeTime || a.step.Kind == domain.StepExternalLunch {
			step.Pause = a.step.VisitEndAt.Sub(a.step.VisitStartAt)
			if a.step.Lunch != nil {
				step.Pause = a.step.Lunch.Duration
			}
			step.NotBefore = a.step.VisitStartAt
			if a.step.ExternalVenue != nil {
				pos := a.step.ExternalVenue.Position
				step.Venue = &pos
			}
		} else if !delayed {
			step.NotBefore = a.step.VisitStartAt
		}
		steps = append(steps, step)
	}
	return steps, order
}

func isCopyTrigger(trigger domain.Trigger) bool {
	_, ok := trigger.(domain.CopyTrigger)
	return ok
}

func (w *rework) points(req *domain.RecomputeRequest, catalog []domain.Candidate) []domain.Coordinate {
	out := []domain.Coordinate{w.position}
	if req.Base.Destination != nil {
		out = append(out, *req.Base.Destination)
	}
	for i := range catalog {
		out = append(out, catalog[i].Place.Location)
	}
	for i := range w.future {
		if e := w.future[i].step.ExternalVenue; e != nil {
			out = append(out, e.Position)
		}
	}
	slices.SortFunc(out, func(a, b domain.Coordinate) int {
		if c := cmp.Compare(a.Longitude, b.Longitude); c != 0 {
			return c
		}
		return cmp.Compare(a.Latitude, b.Latitude)
	})
	return slices.Compact(out)
}

func (w *rework) recordChanges(plan *domain.Plan) {
	for i := range plan.Steps {
		w.recordStepChange(&plan.Steps[i])
	}
	if len(w.changes) > 0 {
		return
	}
	w.recordLegChanges(plan)
}

func (w *rework) recordStepChange(step *domain.Step) {
	i := slices.IndexFunc(w.req.Base.Steps, func(s domain.Step) bool { return s.VisitID == step.VisitID })
	if i < 0 {
		return
	}
	old := &w.req.Base.Steps[i]
	costChanged := old.Cost != nil && step.Cost != nil &&
		(!reflect.DeepEqual(old.Cost.Price, step.Cost.Price) ||
			!reflect.DeepEqual(old.Cost.PersonalAmount, step.Cost.PersonalAmount) ||
			!reflect.DeepEqual(old.Cost.ProgramAmount, step.Cost.ProgramAmount))
	changed := costChanged || stepTimingOrMetaChanged(old, step)
	if !changed ||
		slices.ContainsFunc(
			w.changes,
			func(c domain.RouteChange) bool { return c.BeforeVisitID != nil && *c.BeforeVisitID == step.VisitID },
		) {
		return
	}
	id := step.VisitID
	c := domain.RouteChange{
		Kind:          domain.ChangeKept,
		Scope:         domain.ScopeVisit,
		BeforeVisitID: &id,
		AfterVisitID:  &id,
		Message:       "The retained visit has updated times or catalog information",
	}
	if costChanged {
		before, bok := old.Cost.Price.UpperBound()
		after, aok := step.Cost.Price.UpperBound()
		if bok && aok {
			c.Kind = domain.ChangeCostChanged
			c.Details = domain.CostChange{Before: before, After: after}
			c.Message = "The visit price changed"
		}
	}
	w.changes = append(w.changes, c)
}

func stepTimingOrMetaChanged(old, step *domain.Step) bool {
	return !old.ArrivalAt.Equal(step.ArrivalAt) ||
		!old.VisitStartAt.Equal(step.VisitStartAt) ||
		!old.VisitEndAt.Equal(step.VisitEndAt) ||
		!old.DepartureAt.Equal(step.DepartureAt) ||
		old.Pinned != step.Pinned ||
		old.Obligation != step.Obligation ||
		!reflect.DeepEqual(old.Catalog, step.Catalog) ||
		!reflect.DeepEqual(old.Lunch, step.Lunch) ||
		!reflect.DeepEqual(old.ExternalVenue, step.ExternalVenue)
}

func (w *rework) recordLegChanges(plan *domain.Plan) {
	for i := range plan.Legs {
		if i >= len(w.req.Base.Legs) {
			continue
		}
		l := &plan.Legs[i]
		old := &w.req.Base.Legs[i]
		if !l.DepartureAt.Equal(old.DepartureAt) || !l.ArrivalAt.Equal(old.ArrivalAt) || l.Mode != old.Mode ||
			l.Verification != old.Verification ||
			!slices.Equal(l.Geometry, old.Geometry) {
			id := plan.Steps[min(i, len(plan.Steps)-1)].VisitID
			w.changes = append(
				w.changes,
				domain.RouteChange{
					Kind:          domain.ChangeKept,
					Scope:         domain.ScopeVisit,
					BeforeVisitID: &id,
					AfterVisitID:  &id,
					Message:       "The travel connected to this visit changed",
				},
			)
			return
		}
	}
}
