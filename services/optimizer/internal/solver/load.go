package solver

import (
	"errors"
	"math"
	"slices"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// LoadProfile is the pace the user asked for. Every limit in it is soft: going over costs score, never feasibility.
type LoadProfile struct {
	// One visit per this many hours of the interval; zero sets no norm.
	VisitHours float64
	// Total walking between stops and to the destination; zero sets no limit.
	WalkLimit time.Duration
	// How far each open-hours visit moves from its recommended length toward its minimum, from 0 to 1;
	// a brisker pace fits more places into the same day.
	VisitTrim float64
	// A rest after every this many visits; zero means no rests.
	RestEvery       int
	Rest            time.Duration
	OverVisitWeight float64
	OverWalkWeight  float64
}

const (
	overVisitWeight = 60
	overWalkWeight  = 1
	// Skipping a rest that is due is allowed, but it has to buy more than a quarter hour of rest is worth.
	restSkipWeight = 15
)

// ProfileFor treats any name it does not know as moderate, so clients can send new names safely.
func ProfileFor(name string) LoadProfile {
	switch name {
	case "relaxed":
		return LoadProfile{VisitHours: 2.5, WalkLimit: 45 * time.Minute, RestEvery: 2, Rest: 15 * time.Minute, OverVisitWeight: overVisitWeight, OverWalkWeight: overWalkWeight}
	case "intense":
		return LoadProfile{VisitHours: 1, VisitTrim: 0.5, OverVisitWeight: overVisitWeight, OverWalkWeight: overWalkWeight}
	default:
		return LoadProfile{VisitHours: 1.5, WalkLimit: 90 * time.Minute, OverVisitWeight: overVisitWeight, OverWalkWeight: overWalkWeight}
	}
}

func (l LoadProfile) Validate() error {
	if !nonNegative(l.VisitHours, l.VisitTrim, l.OverVisitWeight, l.OverWalkWeight) || l.VisitTrim > 1 || l.WalkLimit < 0 || l.RestEvery < 0 || l.Rest < 0 {
		return errors.New("load profile values must be finite and non-negative")
	}
	return nil
}

func (l LoadProfile) visitNorm(interval time.Duration) int {
	if l.VisitHours == 0 {
		return 0
	}
	return max(1, int(math.Floor(interval.Hours()/l.VisitHours)))
}

// trim shortens open-hours visits for a brisker pace; it copies the pool, which is shared between searches.
func (l LoadProfile) trim(pool []domain.Candidate) []domain.Candidate {
	if l.VisitTrim == 0 {
		return pool
	}
	trimmed := slices.Clone(pool)
	for i := range trimmed {
		trimmed[i] = l.trimOne(trimmed[i])
	}
	return trimmed
}

// trimOne is trim for one candidate; a repair uses it so a plan keeps the lengths its search chose.
func (l LoadProfile) trimOne(c domain.Candidate) domain.Candidate {
	w := &c.Window
	if l.VisitTrim == 0 || w.Kind != domain.WindowContinuous {
		return c
	}
	cut := time.Duration(float64(w.RecommendedDuration-w.MinDuration) * l.VisitTrim)
	w.RecommendedDuration = max(w.MinDuration, w.RecommendedDuration-cut.Round(time.Minute))
	return c
}

func (l LoadProfile) overWalk(minutes float64) float64 {
	if l.WalkLimit == 0 {
		return 0
	}
	return l.OverWalkWeight * max(0, minutes-l.WalkLimit.Minutes())
}
