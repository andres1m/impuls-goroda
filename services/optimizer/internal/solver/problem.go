package solver

import (
	"errors"
	"fmt"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/pricing"
)

// Problem is one search run: a single archetype over one planning interval.
type Problem struct {
	Start       time.Time
	End         time.Time
	Origin      domain.Coordinate
	Destination *domain.Coordinate
	Interests   domain.InterestMask
	Archetype   domain.Archetype
	Modes       []domain.MovementMode
	Pricing     pricing.Policy
	// Visits every route must contain.
	Anchors []Anchor
	// Places visited earlier today; the route does not return to them.
	Visited []domain.PlaceID
}

func (p Problem) Validate() error {
	if p.Start.IsZero() || !p.End.After(p.Start) {
		return errors.New("search interval is invalid")
	}
	if err := p.Origin.Validate(); err != nil {
		return err
	}
	if p.Destination != nil {
		if err := p.Destination.Validate(); err != nil {
			return err
		}
	}
	if err := p.Archetype.Validate(); err != nil {
		return err
	}
	if len(p.Modes) == 0 {
		return errors.New("at least one movement mode is required")
	}
	for _, mode := range p.Modes {
		if err := mode.Validate(); err != nil {
			return err
		}
	}
	for _, a := range p.Anchors {
		if err := a.Candidate.Validate(); err != nil {
			return fmt.Errorf("anchor: %w", err)
		}
		if a.Candidate.Session == nil && a.Obligation.VisitID == nil {
			return errors.New("anchor requires a session or a visit")
		}
	}
	return p.Pricing.Validate()
}
