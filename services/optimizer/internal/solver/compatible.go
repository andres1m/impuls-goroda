package solver

import (
	"fmt"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// Compatible reports whether both visits fit into one route in some order, ignoring every
// other visit, the way from the origin and the destination. A route visits a place once.
func (s *Solver) Compatible(p *Problem, a, b *domain.Candidate) (bool, error) {
	if err := p.Validate(); err != nil {
		return false, err
	}
	for i, c := range []*domain.Candidate{a, b} {
		if err := c.Validate(); err != nil {
			return false, fmt.Errorf("candidate %d: %w", i, err)
		}
	}
	if a.Place.ID == b.Place.ID {
		return false, nil
	}
	return s.canFollow(p, a, b) || s.canFollow(p, b, a), nil
}

// canFollow places first as early and as short as possible, which leaves next the most room
// as long as a later departure never arrives earlier.
func (s *Solver) canFollow(p *Problem, first, next *domain.Candidate) bool {
	arrival := later(p.Start, first.Window.Start.Add(-first.Window.ArrivalBuffer))
	slot, ok := s.placement.Place(first, arrival, p.End)
	if !ok {
		return false
	}
	if shortest, shortOK := s.placement.Place(first, arrival, slot.StartAt.Add(first.Window.MinDuration)); shortOK {
		slot = shortest
	}
	leg, ok := s.transit.Estimate(first.Place.Location, next.Place.Location, slot.EndAt, p.Modes)
	if !ok {
		return false
	}
	_, ok = s.placement.Place(next, slot.EndAt.Add(leg.Duration), p.End)
	return ok
}
