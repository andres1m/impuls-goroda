package solver

import (
	"errors"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// Problem is one search run: a single archetype over one planning interval.
type Problem struct {
	Start     time.Time
	End       time.Time
	Origin    domain.Coordinate
	Interests domain.InterestMask
	Archetype domain.Archetype
	Modes     []domain.MovementMode
	Currency  string
}

func (p Problem) Validate() error {
	if p.Start.IsZero() || !p.End.After(p.Start) {
		return errors.New("search interval is invalid")
	}
	if err := p.Origin.Validate(); err != nil {
		return err
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
	return domain.Money{Currency: p.Currency}.Validate()
}
