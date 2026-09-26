package domain

import (
	"maps"
	"slices"
	"time"
)

type TransitEstimate struct {
	Mode           MovementMode
	DistanceMeters float64
	Duration       time.Duration
	Verification   VerificationStatus
}

// SearchVisit is a visit chosen by the search. Cost and visit identity are attached when the plan is built.
type SearchVisit struct {
	// Shared with the candidate pool and never modified.
	Candidate *Candidate
	Transit   TransitEstimate
	ArrivalAt time.Time
	StartAt   time.Time
	EndAt     time.Time
}

// Branch is one partial route in the search. Every collection is owned by the branch,
// so parallel expansion never shares mutable state between branches.
type Branch struct {
	Position       Coordinate
	Now            time.Time
	Visits         []SearchVisit
	VisitedPlaces  map[PlaceID]struct{}
	UsedSessions   map[SessionID]struct{}
	KnownCost      Money
	UnknownCost    bool
	CategoryCounts [CategoryCount]int
	Score          float64
}

func NewBranch(origin Coordinate, start time.Time, currency string) *Branch {
	return &Branch{
		Position:      origin,
		Now:           start,
		VisitedPlaces: make(map[PlaceID]struct{}),
		UsedSessions:  make(map[SessionID]struct{}),
		KnownCost:     Money{Currency: currency},
	}
}

func (b *Branch) Clone() *Branch {
	clone := *b
	clone.Visits = slices.Clone(b.Visits)
	clone.VisitedPlaces = maps.Clone(b.VisitedPlaces)
	clone.UsedSessions = maps.Clone(b.UsedSessions)
	return &clone
}

func (b *Branch) CountCategory(c Category) int {
	if i := c.Index(); i >= 0 {
		return b.CategoryCounts[i]
	}
	return 0
}

func (b *Branch) AddCategory(c Category) {
	if i := c.Index(); i >= 0 {
		b.CategoryCounts[i]++
	}
}
