package domain

import (
	"reflect"
	"testing"
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
	b.Steps = []Step{plan.Steps[0]}
	b.Legs = []Leg{plan.Legs[0]}
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

	clone.Steps[0].VisitEndAt = at(12, 0)
	clone.Steps[0].Catalog.Title = "Changed"
	clone.Steps[0].Cost.PersonalAmount.AmountMinor = 1
	clone.Steps[0].AppliedConstraints[0].Code = "CHANGED"
	clone.Steps = append(clone.Steps, freeTimeStep())
	clone.Legs[0].Geometry[0].Latitude = 1
	*clone.Legs[0].DistanceMeters = 1
	clone.Legs[0].Evidence.Limitations = append(clone.Legs[0].Evidence.Limitations, "changed")
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

func TestBranchCloneCopiesNestedPointers(t *testing.T) {
	parent := populatedBranch()
	clone := parent.Clone()
	if clone.Steps[0].Catalog == parent.Steps[0].Catalog || clone.Steps[0].Cost == parent.Steps[0].Cost ||
		clone.Steps[0].Catalog.EventID == parent.Steps[0].Catalog.EventID ||
		clone.Legs[0].DistanceMeters == parent.Legs[0].DistanceMeters ||
		clone.Legs[0].ToVisitID == parent.Legs[0].ToVisitID {
		t.Fatal("clone shares a pointer with its parent")
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
