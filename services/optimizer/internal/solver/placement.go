package solver

import (
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// Placement decides when a visit happens if the user arrives at arrivalAt.
type Placement interface {
	Place(c *domain.Candidate, arrivalAt time.Time, p Problem) (startAt, endAt time.Time, ok bool)
}

// BasicPlacement ignores arrival buffers, last entry and shortening to the minimum duration.
type BasicPlacement struct{}

func (BasicPlacement) Place(c *domain.Candidate, arrivalAt time.Time, p Problem) (startAt, endAt time.Time, ok bool) {
	w := c.Window
	switch w.Kind {
	case domain.WindowFixed:
		if arrivalAt.After(w.Start) {
			return time.Time{}, time.Time{}, false
		}
		startAt, endAt = w.Start, w.End
	case domain.WindowContinuous:
		startAt = w.Start
		if arrivalAt.After(startAt) {
			startAt = arrivalAt
		}
		endAt = startAt.Add(w.RecommendedDuration)
		if endAt.After(w.End) {
			return time.Time{}, time.Time{}, false
		}
	default:
		return time.Time{}, time.Time{}, false
	}
	if endAt.After(p.End) {
		return time.Time{}, time.Time{}, false
	}
	return startAt, endAt, true
}
