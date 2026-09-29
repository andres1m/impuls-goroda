package solver

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// Anchor is a visit the user committed to, such as a purchased session. Its window already
// carries the larger of the session's and the obligation's arrival buffers.
type Anchor struct {
	Candidate  domain.Candidate
	Obligation domain.Obligation
}

// AnchorsFor turns obligations into anchors over the candidate pool. An obligation that cannot
// be honoured becomes a conflict: the route never drops it silently.
func AnchorsFor(obligations []domain.Obligation, pool []domain.Candidate) ([]Anchor, []domain.Conflict) {
	var anchors []Anchor
	var conflicts []domain.Conflict
	seen := make(map[domain.SessionID]struct{}, len(obligations))
	for _, o := range obligations {
		if o.SessionID != nil {
			if _, repeated := seen[*o.SessionID]; repeated {
				continue
			}
			seen[*o.SessionID] = struct{}{}
		}
		if o.SessionID == nil {
			conflicts = append(conflicts, obligationConflict("OBLIGATION_UNKNOWN",
				"The obligation refers to a visit of another plan; pin a session instead", o))
			continue
		}
		i := slices.IndexFunc(
			pool,
			func(c domain.Candidate) bool { return c.Session != nil && c.Session.ID == *o.SessionID },
		)
		if i < 0 {
			conflicts = append(conflicts, obligationConflict("OBLIGATION_UNAVAILABLE",
				"The session is not in the current catalog; choose another session or unpin it", o))
			continue
		}
		c := pool[i]
		switch {
		case c.Session.Availability == domain.AvailabilityCancelled:
			conflicts = append(conflicts, obligationConflict("OBLIGATION_CANCELLED",
				"The session was cancelled; choose another session or unpin it", o))
			continue
		case c.Session.Availability == domain.AvailabilitySoldOut && !holdsPlace(o.Participation):
			conflicts = append(conflicts, obligationConflict("OBLIGATION_SOLD_OUT",
				"No places are left for the session; choose another session or unpin it", o))
			continue
		case o.StartsAt != nil && !o.StartsAt.Equal(c.Window.Start):
			conflicts = append(conflicts, obligationConflict(
				"OBLIGATION_TIME_CHANGED",
				"The session now starts at a different time than when it was pinned; confirm the new time or unpin it",
				o,
			))
			continue
		}
		window := c.Window
		window.ArrivalBuffer = max(window.ArrivalBuffer, o.ArrivalBuffer)
		session := *c.Session
		session.Window = window
		c.Window, c.Session = window, &session
		anchors = append(anchors, Anchor{Candidate: c, Obligation: o})
	}
	return anchors, conflicts
}

// holdsPlace is true when the user reports or the provider confirms a ticket or registration,
// so a sold-out session does not take it away.
func holdsPlace(status domain.ParticipationStatus) bool {
	return status == domain.ParticipationUserReported || status == domain.ParticipationProviderConfirmed
}

func obligationConflict(code, message string, obligations ...domain.Obligation) domain.Conflict {
	c := domain.Conflict{Code: code, Message: message}
	for _, o := range obligations {
		if o.VisitID != nil {
			c.VisitIDs = append(c.VisitIDs, *o.VisitID)
		}
		if o.SessionID != nil {
			c.SessionIDs = append(c.SessionIDs, *o.SessionID)
		}
	}
	return c
}

// Diagnose finds anchors that no route can honour, each on its own or in pairs, before any search.
// A route through all anchors may still not exist when every pair fits; the search then finds none.
func (s *Solver) Diagnose(ctx context.Context, p *Problem) ([]domain.Conflict, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("diagnose context: %w", err)
	}
	run := &searchRun{Solver: s, problem: *p}
	conflicts := s.diagnoseSingleAnchors(p, run)
	pairConflicts, err := s.diagnoseAnchorPairs(p)
	if err != nil {
		return nil, err
	}
	return append(conflicts, pairConflicts...), nil
}

func (s *Solver) diagnoseSingleAnchors(p *Problem, run *searchRun) []domain.Conflict {
	nothingSpent := domain.Money{Currency: p.Pricing.Currency}
	var conflicts []domain.Conflict
	for i := range p.Anchors {
		a := &p.Anchors[i]
		if q, ok := p.Pricing.Quote(&a.Candidate); !ok || !p.Pricing.Fits(nothingSpent, q) {
			conflicts = append(conflicts, obligationConflict("OBLIGATION_OVER_BUDGET",
				"The session's price does not fit the budget or payment rules; relax them or unpin it", a.Obligation))
		}
		if _, ok := run.reach(p.Origin, p.Start, &a.Candidate); !ok {
			conflicts = append(conflicts, obligationConflict(
				"OBLIGATION_UNREACHABLE",
				"The session cannot be reached in time from the start, or it ends after the day ends; change the interval or unpin it",
				a.Obligation,
			))
		}
	}
	return conflicts
}

func (s *Solver) diagnoseAnchorPairs(p *Problem) ([]domain.Conflict, error) {
	var conflicts []domain.Conflict
	for i := range p.Anchors {
		for j := i + 1; j < len(p.Anchors); j++ {
			a, b := &p.Anchors[i], &p.Anchors[j]
			if a.Candidate.Place.ID == b.Candidate.Place.ID {
				conflicts = append(conflicts, obligationConflict(
					"OBLIGATIONS_SAME_PLACE",
					"Two committed sessions are at the same place, and a route visits a place once; keep one of them",
					a.Obligation,
					b.Obligation,
				))
				continue
			}
			ok, err := s.Compatible(p, &a.Candidate, &b.Candidate)
			if err != nil {
				return nil, err
			}
			if !ok {
				conflicts = append(conflicts, obligationConflict(
					"OBLIGATIONS_OVERLAP",
					"The obligations cannot both fit into the day; choose another session for one of them or unpin it",
					a.Obligation,
					b.Obligation,
				))
			}
		}
	}
	return conflicts, nil
}

// mergeAnchors returns the pool with the anchors in place of every catalog candidate at their
// places, since a route visits a place once, and the anchors' indices in the merged pool.
func mergeAnchors(pool []domain.Candidate, anchors []Anchor) (merged []domain.Candidate, indices []int) {
	if len(anchors) == 0 {
		return pool, nil
	}
	merged = slices.DeleteFunc(slices.Clone(pool), func(c domain.Candidate) bool {
		return slices.ContainsFunc(anchors, func(a Anchor) bool { return a.Candidate.Place.ID == c.Place.ID })
	})
	indices = make([]int, len(anchors))
	for i := range anchors {
		indices[i] = len(merged)
		merged = append(merged, anchors[i].Candidate)
	}
	return merged, indices
}

// fixedTime is true for an anchor whose visit cannot move: its place among the anchors of any
// route follows from its start alone.
func fixedTime(c *domain.Candidate) bool {
	late := c.Window.LateEntryAllowed != nil && *c.Window.LateEntryAllowed
	return c.Window.Kind == domain.WindowFixed && !late
}

// anchorsReachable is a necessary condition for the branch to still honour every anchor ahead,
// so it never discards a branch that could: the known prices of the anchors left still fit a
// strict budget; the fixed-time anchors fit one after another in the order they start; and each
// flexible anchor, staying as briefly as allowed, fits into some gap of that chain so that the
// next fixed-time anchor is still reached.
func (r *searchRun) anchorsReachable(b *domain.Branch) bool {
	if !r.anchorsAffordable(b) {
		return false
	}
	gaps := []gap{{b.Position, b.Now}}
	var ahead []*domain.Candidate
	for _, i := range r.fixedAnchors {
		c := &r.pool[i]
		if done(b, c) {
			continue
		}
		last := gaps[len(gaps)-1]
		end, ok := r.reach(last.position, last.now, c)
		if !ok {
			return false
		}
		ahead = append(ahead, c)
		gaps = append(gaps, gap{c.Place.Location, end})
	}
	for _, i := range r.flexibleAnchors {
		flexible := &r.pool[i]
		if done(b, flexible) {
			continue
		}
		if !r.fitsAGap(flexible, gaps, ahead) {
			return false
		}
	}
	return true
}

// gap is where and when the user stands before the next fixed-time anchor, or after the last one.
type gap struct {
	position domain.Coordinate
	now      time.Time
}

// fitsAGap tells whether the flexible anchor fits into gap k before fixed-time anchor ahead[k],
// or into the last gap after all of them.
func (r *searchRun) fitsAGap(flexible *domain.Candidate, gaps []gap, ahead []*domain.Candidate) bool {
	for k, g := range gaps {
		end, ok := r.reachBriefly(g.position, g.now, flexible)
		if !ok {
			continue
		}
		if k == len(ahead) {
			return true
		}
		if _, ok := r.reach(flexible.Place.Location, end, ahead[k]); ok {
			return true
		}
	}
	return false
}

// anchorsAffordable checks a strict budget against what the branch spent plus the known prices
// of the anchors it still has to visit.
func (r *searchRun) anchorsAffordable(b *domain.Branch) bool {
	budget := r.problem.Pricing.Budget
	if budget.Mode != domain.BudgetStrict {
		return true
	}
	spent := b.KnownCost.AmountMinor
	for _, i := range r.anchors {
		if done(b, &r.pool[i]) {
			continue
		}
		if upper, known := r.quotes[i].Price.UpperBound(); known {
			spent = saturatingAdd(spent, upper.AmountMinor)
		}
	}
	return spent <= budget.Limit.AmountMinor
}

// complete is true for a route with a visit, every anchor and the lunch the problem asks for.
func (r *searchRun) complete(b *domain.Branch) bool {
	if len(b.Visits) == 0 || r.lunchPending(b) {
		return false
	}
	for _, i := range r.anchors {
		if !done(b, &r.pool[i]) {
			return false
		}
	}
	return true
}

// Another session at the same venue does not satisfy a session commitment.
func done(b *domain.Branch, anchor *domain.Candidate) bool {
	if anchor.Session == nil {
		_, used := b.VisitedPlaces[anchor.Place.ID]
		return used
	}
	_, used := b.UsedSessions[anchor.Session.ID]
	return used
}

// reach returns when a visit to c ends at the earliest if the user leaves position at now.
func (r *searchRun) reach(position domain.Coordinate, now time.Time, c *domain.Candidate) (time.Time, bool) {
	leg, ok := r.transit.Estimate(position, c.Place.Location, now, r.problem.Modes)
	if !ok {
		return time.Time{}, false
	}
	slot, _, ok := r.place(c, now.Add(leg.Duration))
	return slot.EndAt, ok
}

// reachBriefly is reach with the shortest stay the visit's window allows.
func (r *searchRun) reachBriefly(position domain.Coordinate, now time.Time, c *domain.Candidate) (time.Time, bool) {
	leg, ok := r.transit.Estimate(position, c.Place.Location, now, r.problem.Modes)
	if !ok {
		return time.Time{}, false
	}
	arrival := now.Add(leg.Duration)
	slot, _, ok := r.place(c, arrival)
	if !ok {
		return time.Time{}, false
	}
	if shortest, ok := r.placement.Place(c, arrival, slot.StartAt.Add(c.Window.MinDuration)); ok {
		slot = shortest
	}
	return slot.EndAt, true
}
