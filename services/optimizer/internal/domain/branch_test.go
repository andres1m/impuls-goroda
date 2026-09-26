package domain

import (
	"reflect"
	"testing"
	"time"
)

func TestNewBranch(t *testing.T) {
	origin := Coordinate{Longitude: 37.59, Latitude: 55.69}
	b := NewBranch(origin, at(9, 0), rub)
	if b.Position != origin || !b.Now.Equal(at(9, 0)) {
		t.Fatalf("branch starts at %+v %s", b.Position, b.Now)
	}
	if b.VisitedPlaces == nil || b.UsedSessions == nil {
		t.Fatal("branch sets are not initialised")
	}
	if b.KnownCost != money(0) || b.UnknownCost {
		t.Fatalf("branch cost = %+v unknown=%v", b.KnownCost, b.UnknownCost)
	}
}

func populatedBranch() *Branch {
	plan := validPlan()
	b := NewBranch(plan.Origin, plan.Start, rub)
	candidate := validEventCandidate()
	b.Visits = []SearchVisit{{
		Candidate: &candidate,
		Transit:   TransitEstimate{Mode: MovementWalk, DistanceMeters: 800, Duration: 13 * time.Minute, Verification: VerificationEstimated},
		ArrivalAt: at(10, 13),
		StartAt:   at(10, 13),
		EndAt:     at(11, 0),
	}}
	b.Position = Coordinate{Longitude: 37.6, Latitude: 55.7}
	b.Now = at(11, 0)
	b.VisitedPlaces[PlaceID{1}] = struct{}{}
	b.UsedSessions[SessionID{3}] = struct{}{}
	b.KnownCost = money(50000)
	b.AddCategory(CategoryCulture)
	b.Score = 3.5
	return b
}

func TestBranchCloneIsIndependent(t *testing.T) {
	parent := populatedBranch()
	snapshot := populatedBranch()
	clone := parent.Clone()

	if !reflect.DeepEqual(parent, clone) {
		t.Fatal("clone differs from its parent before any change")
	}

	clone.Visits[0].EndAt = at(12, 0)
	clone.Visits[0].Transit.Mode = MovementTransit
	clone.Visits = append(clone.Visits, clone.Visits[0])
	clone.VisitedPlaces[PlaceID{9}] = struct{}{}
	clone.UsedSessions[SessionID{9}] = struct{}{}
	clone.AddCategory(CategoryCulture)
	clone.KnownCost.AmountMinor = 1
	clone.UnknownCost = true
	clone.Score = 0
	clone.Now = at(15, 0)

	if !reflect.DeepEqual(parent, snapshot) {
		t.Fatal("changing the clone changed its parent")
	}
}

func TestBranchCloneSharesCandidates(t *testing.T) {
	parent := populatedBranch()
	if parent.Clone().Visits[0].Candidate != parent.Visits[0].Candidate {
		t.Fatal("clone copied a read-only candidate")
	}
}

func TestBranchCategoryCounts(t *testing.T) {
	a := NewBranch(Coordinate{}, at(9, 0), rub)
	b := a.Clone()
	a.AddCategory(CategoryGastro)
	a.AddCategory(CategoryGastro)
	if a.CountCategory(CategoryGastro) != 2 || b.CountCategory(CategoryGastro) != 0 {
		t.Fatalf("counts = %d and %d, want 2 and 0", a.CountCategory(CategoryGastro), b.CountCategory(CategoryGastro))
	}
	if a.CountCategory(CategorySport) != 0 {
		t.Fatal("unrelated category counted")
	}
}

func TestBranchIgnoresUnknownCategory(t *testing.T) {
	b := NewBranch(Coordinate{}, at(9, 0), rub)
	b.AddCategory("nightlife")
	if b.CountCategory("nightlife") != 0 || b.CategoryCounts != [CategoryCount]int{} {
		t.Fatal("unknown category changed the counters")
	}
}

func TestBranchCloneKeepsNilCollections(t *testing.T) {
	b := NewBranch(Coordinate{}, at(9, 0), rub)
	if clone := b.Clone(); !reflect.DeepEqual(b, clone) {
		t.Fatalf("clone of an empty branch differs: %+v vs %+v", b, clone)
	}
}
