package solver

import (
	"errors"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// LunchSlot is the lunch a route reserves: Duration somewhere between Start and End.
type LunchSlot struct {
	Start    time.Time
	End      time.Time
	Duration time.Duration
}

const (
	shortestLunch = 45 * time.Minute
	longestLunch  = time.Hour
)

// lunchRadii are searched from the nearest: the first ring with a venue decides where lunch is.
var lunchRadii = []float64{300, 500, 800, 1000}

// LunchSlotFor reserves 45 to 60 minutes, or the whole window when the user gave a shorter one.
func LunchSlotFor(w domain.LunchWindow) LunchSlot {
	return LunchSlot{Start: w.Start, End: w.End, Duration: min(max(w.MinDuration, shortestLunch), longestLunch, w.End.Sub(w.Start))}
}

func (l LunchSlot) Validate() error {
	if l.Start.IsZero() || l.Duration <= 0 || l.End.Sub(l.Start) < l.Duration {
		return errors.New("lunch slot is invalid")
	}
	return nil
}

// lunchVenue is a place to eat that can host the whole lunch and nothing longer.
func lunchVenue(c *domain.Candidate, lunch time.Duration) bool {
	return c.Event == nil && c.Category() == domain.CategoryGastro &&
		c.Window.Kind == domain.WindowContinuous && c.Window.MinDuration <= lunch
}

// lunchPending is true when the branch has yet to reserve the problem's lunch.
func (r searchRun) lunchPending(b *domain.Branch) bool {
	return r.problem.Lunch != nil && b.Lunch == nil
}

// lunchStillFits is false for a branch that has left no time for lunch.
func (r searchRun) lunchStillFits(b *domain.Branch) bool {
	return !r.lunchPending(b) || !b.Now.After(r.problem.Lunch.End.Add(-r.problem.Lunch.Duration))
}

// lunches are the ways the branch can have lunch next: at the venues of the nearest ring that has
// one, or, with no venue within the widest ring, as a pause where the user stands.
func (r searchRun) lunches(parent *domain.Branch) []*domain.Branch {
	if !r.lunchPending(parent) || !r.lunchStillFits(parent) {
		return nil
	}
	type venue struct {
		branch   *domain.Branch
		distance float64
	}
	var venues []venue
	widest := lunchRadii[len(lunchRadii)-1]
	for _, i := range r.lunchVenues {
		d := distanceMeters(parent.Position, r.pool[i].Place.Location)
		if d > widest {
			continue
		}
		if child, ok := r.lunchAt(parent, i); ok {
			venues = append(venues, venue{child, d})
		}
	}
	for _, radius := range lunchRadii {
		var ring []*domain.Branch
		for _, v := range venues {
			if v.distance <= radius {
				ring = append(ring, v.branch)
			}
		}
		if len(ring) > 0 {
			return ring
		}
	}
	if child, ok := r.lunchPause(parent); ok {
		return []*domain.Branch{child}
	}
	return nil
}

// lunchAt has lunch at venue i for exactly the lunch's duration within both the lunch window and
// the venue's own hours.
func (r searchRun) lunchAt(parent *domain.Branch, i int) (*domain.Branch, bool) {
	c := &r.pool[i]
	if _, visited := parent.VisitedPlaces[c.Place.ID]; visited || !r.priced[i] || !r.problem.Pricing.Fits(parent.KnownCost, r.quotes[i]) {
		return nil, false
	}
	leg, ok := r.transit.Estimate(parent.Position, c.Place.Location, parent.Now, r.problem.Modes)
	if !ok {
		return nil, false
	}
	l := r.problem.Lunch
	lunch := *c
	lunch.Window.Start, lunch.Window.End = later(c.Window.Start, l.Start), earlier(c.Window.End, l.End)
	lunch.Window.MinDuration, lunch.Window.RecommendedDuration = l.Duration, l.Duration
	arrival := parent.Now.Add(leg.Duration)
	slot, finish, ok := r.placeBy(&lunch, arrival, r.problem.End)
	if !ok {
		return nil, false
	}
	visit := domain.SearchVisit{Candidate: c, Transit: leg, ArrivalAt: arrival, Buffer: slot.Buffer, StartAt: slot.StartAt, EndAt: slot.EndAt}
	child := r.extend(parent, visit, finish, r.utilities[i], r.quotes[i])
	child.Lunch = &domain.Lunch{At: len(child.Visits) - 1, Venue: true, StartAt: slot.StartAt, EndAt: slot.EndAt}
	return child, r.anchorsReachable(child)
}

// lunchPause keeps the lunch time free where the user stands; it earns nothing and counts in no
// category, while the wait before it is still waiting.
func (r searchRun) lunchPause(parent *domain.Branch) (*domain.Branch, bool) {
	l := r.problem.Lunch
	start := later(parent.Now, l.Start)
	end := start.Add(l.Duration)
	if end.After(l.End) || end.After(r.problem.End) {
		return nil, false
	}
	var finish *domain.TransitEstimate
	if r.problem.Destination != nil {
		leg, ok := r.transit.Estimate(parent.Position, *r.problem.Destination, end, r.problem.Modes)
		if !ok || end.Add(leg.Duration).After(r.problem.End) {
			return nil, false
		}
		finish = &leg
	}
	child := parent.Clone()
	// The user stays put, so only the time the leg to the destination starts at can change.
	child.Score += r.finishPenalty(parent.Position, parent.Finish) - r.finishPenalty(parent.Position, finish) - r.score.WaitWeight*start.Sub(parent.Now).Minutes()
	child.Finish = finish
	child.Now = end
	child.Lunch = &domain.Lunch{At: len(child.Visits), StartAt: start, EndAt: end}
	return child, r.anchorsReachable(child)
}

func compareLunches(a, b *domain.Lunch) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	}
	if a.At != b.At {
		return a.At - b.At
	}
	return a.StartAt.Compare(b.StartAt)
}
