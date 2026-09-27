package solver

import (
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// Slot is when a visit happens.
type Slot struct {
	StartAt time.Time
	EndAt   time.Time
	Buffer  time.Duration
}

// Placement decides when a visit happens if the user arrives at arrivalAt and must be done by deadline.
type Placement interface {
	Place(c *domain.Candidate, arrivalAt, deadline time.Time) (Slot, bool)
}

// WindowPlacement never moves a fixed session and never shortens a visit below its minimum.
type WindowPlacement struct{}

func (WindowPlacement) Place(c *domain.Candidate, arrivalAt, deadline time.Time) (Slot, bool) {
	w := c.Window
	switch w.Kind {
	case domain.WindowFixed:
		return placeFixed(w, arrivalAt, deadline)
	case domain.WindowContinuous:
		return placeContinuous(w, arrivalAt, deadline)
	default:
		return Slot{}, false
	}
}

func placeFixed(w domain.VisitWindow, arrivalAt, deadline time.Time) (Slot, bool) {
	if w.End.After(deadline) || w.End.Sub(w.Start) < w.MinDuration {
		return Slot{}, false
	}
	if !arrivalAt.Add(w.ArrivalBuffer).After(w.Start) {
		return Slot{StartAt: w.Start, EndAt: w.End, Buffer: w.ArrivalBuffer}, true
	}
	if w.LateEntryAllowed == nil || !*w.LateEntryAllowed {
		return Slot{}, false
	}
	// A late entrant walks straight in, so the buffer that protects an on-time start no longer applies.
	start := later(arrivalAt, w.Start)
	lastEntry := w.End.Add(-w.MinDuration)
	if w.LastEntryAt != nil {
		lastEntry = *w.LastEntryAt
	}
	if start.After(lastEntry) || w.End.Sub(start) < w.MinDuration {
		return Slot{}, false
	}
	return Slot{StartAt: start, EndAt: w.End}, true
}

func placeContinuous(w domain.VisitWindow, arrivalAt, deadline time.Time) (Slot, bool) {
	start := later(arrivalAt.Add(w.ArrivalBuffer), w.Start)
	if w.LastEntryAt != nil && start.After(*w.LastEntryAt) {
		return Slot{}, false
	}
	latestEnd := w.End
	if deadline.Before(latestEnd) {
		latestEnd = deadline
	}
	duration := min(w.RecommendedDuration, latestEnd.Sub(start))
	if duration < w.MinDuration {
		return Slot{}, false
	}
	return Slot{StartAt: start, EndAt: start.Add(duration), Buffer: w.ArrivalBuffer}, true
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
