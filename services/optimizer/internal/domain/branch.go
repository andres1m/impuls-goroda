package domain

import (
	"maps"
	"slices"
	"time"
)

// Branch is one partial route in the search. Every collection is owned by the branch,
// so parallel expansion never shares mutable state between branches.
type Branch struct {
	Position       Coordinate
	Now            time.Time
	Steps          []Step
	Legs           []Leg
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
	clone.Steps = cloneEach(b.Steps, cloneStep)
	clone.Legs = cloneEach(b.Legs, cloneLeg)
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

func cloneEach[T any](items []T, clone func(T) T) []T {
	if items == nil {
		return nil
	}
	out := make([]T, len(items))
	for i, item := range items {
		out[i] = clone(item)
	}
	return out
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func cloneStep(s Step) Step {
	if s.Catalog != nil {
		catalog := *s.Catalog
		catalog.EntranceID = clonePtr(catalog.EntranceID)
		catalog.EventID = clonePtr(catalog.EventID)
		catalog.SessionID = clonePtr(catalog.SessionID)
		catalog.SessionStart = clonePtr(catalog.SessionStart)
		catalog.SessionEnd = clonePtr(catalog.SessionEnd)
		catalog.Provenance = cloneProvenance(catalog.Provenance)
		s.Catalog = &catalog
	}
	if s.Cost != nil {
		cost := cloneCost(*s.Cost)
		s.Cost = &cost
	}
	s.AppliedConstraints = slices.Clone(s.AppliedConstraints)
	return s
}

func cloneLeg(l Leg) Leg {
	l.FromVisitID = clonePtr(l.FromVisitID)
	l.ToVisitID = clonePtr(l.ToVisitID)
	l.DistanceMeters = clonePtr(l.DistanceMeters)
	l.Geometry = slices.Clone(l.Geometry)
	l.Evidence.Limitations = slices.Clone(l.Evidence.Limitations)
	l.Cost = cloneCost(l.Cost)
	return l
}

func cloneCost(c CostSnapshot) CostSnapshot {
	c.PriceOfferID = clonePtr(c.PriceOfferID)
	c.Price.LowerMinor = clonePtr(c.Price.LowerMinor)
	c.Price.UpperMinor = clonePtr(c.Price.UpperMinor)
	c.PersonalAmount = clonePtr(c.PersonalAmount)
	c.ProgramAmount = clonePtr(c.ProgramAmount)
	c.UnknownComponents = slices.Clone(c.UnknownComponents)
	c.Provenance = cloneProvenance(c.Provenance)
	return c
}

func cloneProvenance(p Provenance) Provenance {
	p.SourceURL = clonePtr(p.SourceURL)
	p.SourceRecordID = clonePtr(p.SourceRecordID)
	p.SourceUpdatedAt = clonePtr(p.SourceUpdatedAt)
	p.VerifiedAt = clonePtr(p.VerifiedAt)
	return p
}
