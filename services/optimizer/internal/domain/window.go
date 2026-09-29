package domain

import (
	"errors"
	"time"
)

type WindowKind string

const (
	WindowFixed      WindowKind = "fixed"
	WindowContinuous WindowKind = "continuous"
)

// VisitWindow is when a visit may happen, whether it comes from a session or a place's opening hours.
type VisitWindow struct {
	Kind                WindowKind    `json:"Kind"`
	Start               time.Time     `json:"Start"`
	End                 time.Time     `json:"End"`
	LastEntryAt         *time.Time    `json:"LastEntryAt"`
	MinDuration         time.Duration `json:"MinDuration"`
	RecommendedDuration time.Duration `json:"RecommendedDuration"`
	ArrivalBuffer       time.Duration `json:"ArrivalBuffer"`
	// Nil means the source never confirmed late entry, so it is not allowed.
	LateEntryAllowed *bool `json:"LateEntryAllowed"`
	// The place gave no opening hours, so the window is assumed and the user has to check it.
	HoursUnknown bool `json:"HoursUnknown"`
}

func (w *VisitWindow) Validate() error {
	if w.Kind != WindowFixed && w.Kind != WindowContinuous {
		return errors.New("invalid visit window kind")
	}
	if w.Start.IsZero() || !w.End.After(w.Start) {
		return errors.New("visit window interval is invalid")
	}
	if w.MinDuration <= 0 || w.RecommendedDuration < w.MinDuration {
		return errors.New("visit window durations are invalid")
	}
	if w.ArrivalBuffer < 0 {
		return errors.New("arrival buffer must not be negative")
	}
	if w.LastEntryAt != nil && (w.LastEntryAt.Before(w.Start) || w.LastEntryAt.After(w.End)) {
		return errors.New("last entry must be inside the visit window")
	}
	if w.Kind == WindowContinuous && w.End.Sub(w.Start) < w.MinDuration {
		return errors.New("visit window is shorter than the minimum duration")
	}
	return nil
}
