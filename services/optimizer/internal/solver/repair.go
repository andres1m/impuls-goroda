package solver

import (
	"context"
	"slices"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// RepairStep is one step of a planned route: a visit, or a pause when Candidate is nil.
type RepairStep struct {
	Candidate *domain.Candidate
	Pause     time.Duration
	// The step does not start before this, so a plan that still works keeps its times.
	NotBefore time.Time
}

// RepairStop is a step the repaired route keeps: its visit, or the pause's new interval.
type RepairStop struct {
	Step       int
	Visit      *domain.SearchVisit
	PauseStart time.Time
	PauseEnd   time.Time
}

type Repair struct {
	Stops []RepairStop
	// Indices of soft steps that no longer fit.
	Dropped []int
	// Leg from the last stop to the destination; nil without a destination.
	Finish *domain.TransitEstimate
	// An anchor the route cannot keep; the repair then has no route.
	Unreachable *Anchor
	// The destination cannot be reached in time even with every soft step left out.
	DestinationUnreachable bool
}

// Repair walks the planned steps in their order from the problem's start. A step that no longer
// fits is shortened to its minimum when it has an open window, and otherwise left out, unless it is
// an anchor: then the repair reports it instead of dropping a commitment. Pauses keep their length.
func (s *Solver) Repair(ctx context.Context, p Problem, steps []RepairStep) (Repair, error) {
	check := p
	if !p.Start.IsZero() && !p.End.IsZero() && !p.End.After(p.Start) {
		check.End = p.Start.Add(time.Minute)
	}
	if err := check.Validate(); err != nil {
		return Repair{}, err
	}
	if err := ctx.Err(); err != nil {
		return Repair{}, err
	}
	run, err := s.newRun(p, nil)
	if err != nil {
		return Repair{}, err
	}
	var out Repair
	b := run.root()
	for i, step := range steps {
		if step.Candidate == nil {
			if next, ok := run.pause(b, step); ok {
				out.Stops = append(out.Stops, RepairStop{Step: i, PauseStart: next.Now.Add(-step.Pause), PauseEnd: next.Now})
				b = next
			} else {
				out.Dropped = append(out.Dropped, i)
			}
			continue
		}
		// A commitment keeps its own window, which may carry a longer arrival buffer than the catalog's.
		a := anchorFor(p.Anchors, step.Candidate)
		if a != nil {
			step.Candidate = &a.Candidate
		}
		trimmed := p.Load.trimOne(*step.Candidate)
		step.Candidate = &trimmed
		next, ok := run.keep(b, step, a != nil)
		if ok {
			out.Stops = append(out.Stops, RepairStop{Step: i, Visit: &next.Visits[len(next.Visits)-1]})
			b = next
			continue
		}
		if a != nil {
			return Repair{Unreachable: a}, nil
		}
		out.Dropped = append(out.Dropped, i)
	}
	for i := range p.Anchors {
		if !done(b, &p.Anchors[i].Candidate) {
			return Repair{Unreachable: &p.Anchors[i]}, nil
		}
	}
	if p.Destination != nil && b.Finish == nil {
		finish, ok := run.transit.Estimate(b.Position, *p.Destination, b.Now, p.Modes)
		if !ok || b.Now.Add(finish.Duration).After(p.End) {
			return Repair{DestinationUnreachable: true}, nil
		}
		b.Finish = &finish
	}
	out.Finish = b.Finish
	return out, nil
}

// keep places the step's visit next, at its usual length or else at its minimum.
func (r searchRun) keep(b *domain.Branch, step RepairStep, anchor bool) (*domain.Branch, bool) {
	c := step.Candidate
	if _, visited := b.VisitedPlaces[c.Place.ID]; visited {
		return nil, false
	}
	if !anchor && slices.ContainsFunc(r.problem.Anchors, func(a Anchor) bool { return a.Candidate.Place.ID == c.Place.ID }) {
		return nil, false
	}
	leg, ok := r.transit.Estimate(b.Position, c.Place.Location, b.Now, r.problem.Modes)
	if !ok {
		return nil, false
	}
	arrival := b.Now.Add(leg.Duration)
	// Arriving by the planned start keeps the planned start even when the user could be earlier.
	placeFrom := arrival
	if !step.NotBefore.IsZero() {
		placeFrom = later(arrival, step.NotBefore.Add(-c.Window.ArrivalBuffer))
	}
	quote, allowed := r.problem.Pricing.Quote(c)
	if !allowed || !r.problem.Pricing.Fits(b.KnownCost, quote) {
		return nil, false
	}
	attempt := func(deadline time.Time) (*domain.Branch, time.Time, bool) {
		slot, finish, ok := r.placeBy(c, placeFrom, deadline)
		if ok && !step.NotBefore.IsZero() && slot.StartAt.Before(step.NotBefore) {
			// Late entry needs no buffer, so the visit could start before its planned time.
			slot, finish, ok = r.placeBy(c, step.NotBefore, deadline)
		}
		if !ok {
			return nil, time.Time{}, false
		}
		visit := domain.SearchVisit{Candidate: c, Transit: leg, ArrivalAt: arrival, Buffer: slot.Buffer, StartAt: slot.StartAt, EndAt: slot.EndAt}
		child := r.extend(b, visit, finish, 0, quote, false)
		return child, slot.StartAt, r.anchorsReachable(child)
	}
	child, start, ok := attempt(r.problem.End)
	if ok {
		return child, true
	}
	if child == nil || c.Window.Kind != domain.WindowContinuous {
		return nil, false
	}
	child, _, ok = attempt(start.Add(c.Window.MinDuration))
	return child, ok
}

// pause keeps a pause of the same length where the user stands, from its planned time or now.
func (r searchRun) pause(b *domain.Branch, step RepairStep) (*domain.Branch, bool) {
	end := later(b.Now, step.NotBefore).Add(step.Pause)
	if step.Pause <= 0 || end.After(r.problem.End) {
		return nil, false
	}
	next := b.Clone()
	next.Now = end
	if r.problem.Destination != nil {
		finish, ok := r.transit.Estimate(b.Position, *r.problem.Destination, end, r.problem.Modes)
		if !ok || end.Add(finish.Duration).After(r.problem.End) {
			return nil, false
		}
		next.Finish = &finish
	}
	return next, r.anchorsReachable(next)
}

func anchorFor(anchors []Anchor, c *domain.Candidate) *Anchor {
	i := slices.IndexFunc(anchors, func(a Anchor) bool {
		if a.Candidate.Session == nil || c.Session == nil {
			return a.Candidate.Session == nil && c.Session == nil && a.Candidate.Place.ID == c.Place.ID
		}
		return a.Candidate.Session.ID == c.Session.ID
	})
	if i < 0 {
		return nil
	}
	return &anchors[i]
}
