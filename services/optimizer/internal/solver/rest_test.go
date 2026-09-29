package solver

import (
	"math"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func restProblem() *Problem {
	p := problem()
	p.Load = LoadProfile{RestEvery: 2, Rest: 15 * time.Minute}
	return p
}

func TestRestAfterEverySecondVisit(t *testing.T) {
	categories := []domain.Category{
		domain.CategoryCulture,
		domain.CategoryWalk,
		domain.CategorySport,
		domain.CategoryTourism,
		domain.CategoryVolunteer,
	}
	var pool []domain.Candidate
	for i := byte(1); i <= 5; i++ {
		pool = append(pool, place(i, categories[i-1], 0, north(origin, float64(i)*100)))
	}
	b := search(t, wide, restProblem(), pool)[0]
	if len(b.Visits) < 3 {
		t.Fatalf("only %d visits", len(b.Visits))
	}
	if len(b.Rests) == 0 || b.Rests[0].At != 2 || b.Rests[0].EndAt.Sub(b.Rests[0].StartAt) != 15*time.Minute ||
		!b.Rests[0].StartAt.Equal(b.Visits[1].EndAt) {
		t.Fatalf("rests %+v", b.Rests)
	}
	if b.Visits[2].ArrivalAt.Before(b.Rests[0].EndAt) {
		t.Fatal("third visit starts before the rest ends")
	}
}

func TestRestYieldsToAnAnchor(t *testing.T) {
	first := place(1, domain.CategoryCulture, 0, north(origin, 100))
	second := place(2, domain.CategoryWalk, 0, north(origin, 200))
	// Right after the second visit there is time to reach the concert, but not with a rest first.
	concert := session(3, domain.CategorySport, north(origin, 300), at(12, 10), at(13, 0))
	p := restProblem()
	p.End = at(13, 10)
	p.Anchors = []Anchor{anchor(&concert)}
	b := search(t, wide, p, []domain.Candidate{first, second, concert})[0]
	if len(b.Visits) != 3 || b.Visits[2].Candidate.Session == nil || len(b.Rests) != 0 {
		t.Fatalf("visits %v rests %+v", placeIDs(b), b.Rests)
	}
	p.Load = LoadProfile{}
	unrested := search(t, wide, p, []domain.Candidate{first, second, concert})[0]
	// With the concert right after, there is no room to rest at all, so leaving the rest out is free.
	if skipped := unrested.Score - b.Score; math.Abs(skipped) > 1e-9 {
		t.Fatalf("skipping a rest that does not fit cost %v", skipped)
	}
}

func TestSkippingARestThatFitsCosts(t *testing.T) {
	first := place(1, domain.CategoryCulture, 0, north(origin, 100))
	second := place(2, domain.CategoryWalk, 0, north(origin, 200))
	// The show is worth the skip, but nothing forces it: resting instead would still leave a valid day.
	show := session(3, domain.CategorySport, north(origin, 300), at(12, 10), at(13, 0))
	p := restProblem()
	p.End = at(13, 10)
	b := search(t, wide, p, []domain.Candidate{first, second, show})[0]
	if len(b.Visits) != 3 || len(b.Rests) != 0 {
		t.Fatalf("visits %v rests %+v", placeIDs(b), b.Rests)
	}
	p.Load = LoadProfile{}
	unrested := search(t, wide, p, []domain.Candidate{first, second, show})[0]
	if skipped := unrested.Score - b.Score; math.Abs(skipped-restSkipWeight) > 1e-9 {
		t.Fatalf("skipping a rest that fits cost %v, want %v", skipped, float64(restSkipWeight))
	}
}

func TestNoRestsWithoutAProfile(t *testing.T) {
	var pool []domain.Candidate
	for i := byte(1); i <= 4; i++ {
		pool = append(
			pool,
			place(
				i,
				[]domain.Category{domain.CategoryCulture, domain.CategoryWalk, domain.CategorySport, domain.CategoryTourism}[i-1],
				0,
				north(origin, float64(i)*100),
			),
		)
	}
	if b := search(t, wide, problem(), pool)[0]; len(b.Rests) != 0 {
		t.Fatalf("rests %+v", b.Rests)
	}
}

// halfHour is a place whose visit always lasts half an hour, so the day's timeline is known in advance.
func halfHour(id byte, category domain.Category, location domain.Coordinate) domain.Candidate {
	c := place(id, category, 0, location)
	c.Window.MinDuration, c.Window.RecommendedDuration = 30*time.Minute, 30*time.Minute
	return c
}

// threeHalfHours are three half-hour visits of different categories a hundred metres apart.
func threeHalfHours() []domain.Candidate {
	categories := []domain.Category{domain.CategoryCulture, domain.CategoryWalk, domain.CategorySport}
	var pool []domain.Candidate
	for i := byte(1); i <= 3; i++ {
		pool = append(pool, halfHour(i, categories[i-1], north(origin, float64(i)*100)))
	}
	return pool
}

// requireFreeSkip checks the rested search keeps three visits without the rest and scores as if no rest was asked for.
func requireFreeSkip(t *testing.T, rested, unrested *Problem, pool []domain.Candidate) {
	t.Helper()
	b := search(t, wide, rested, pool)[0]
	if len(b.Visits) != 3 || len(b.Rests) != 0 {
		t.Fatalf("visits %v rests %+v", placeIDs(b), b.Rests)
	}
	if skipped := search(t, wide, unrested, pool)[0].Score - b.Score; math.Abs(skipped) > 1e-9 {
		t.Fatalf("skipping a rest that does not fit cost %v", skipped)
	}
}

func TestRestYieldsToTheWayToTheDestination(t *testing.T) {
	first := halfHour(1, domain.CategoryCulture, north(origin, 100))
	second := halfHour(2, domain.CategoryWalk, north(origin, 200))
	// The last stop is at the destination and far from the second one.
	dest := north(origin, 2200)
	pool := []domain.Candidate{first, second, halfHour(3, domain.CategorySport, dest)}
	unrested := problem()
	unrested.Destination = &dest
	day := search(t, wide, unrested, pool)[0]
	if len(day.Visits) != 3 {
		t.Fatalf("visits %v", placeIDs(day))
	}
	// The day ends as soon as the three visits and the way to the destination are done.
	unrested.End = day.Visits[2].EndAt.Add(day.Finish.Duration)
	rested := *unrested
	rested.Load = LoadProfile{RestEvery: 2, Rest: 45 * time.Minute}
	restEnd := day.Visits[1].EndAt.Add(rested.Load.Rest)
	if restEnd.After(unrested.End) {
		t.Fatal("the rest itself must fit into the day, or the destination check is never reached")
	}
	requireFreeSkip(t, &rested, unrested, pool)
}

func TestRestYieldsToLunch(t *testing.T) {
	pool := threeHalfHours()
	day := search(t, wide, problem(), pool)[0]
	if len(day.Visits) != 3 {
		t.Fatalf("visits %v", placeIDs(day))
	}
	// Lunch has to start right after the third visit, and the day ends with it.
	lunchAt := day.Visits[2].EndAt
	unrested := problem()
	unrested.Lunch = &LunchSlot{Start: lunchAt, End: lunchAt.Add(45 * time.Minute), Duration: 45 * time.Minute}
	unrested.End = unrested.Lunch.End
	rested := *unrested
	rested.Load = LoadProfile{RestEvery: 2, Rest: 45 * time.Minute}
	if !day.Visits[1].EndAt.Add(rested.Load.Rest).After(lunchAt) {
		t.Fatal("the rest must push lunch past its latest start")
	}
	requireFreeSkip(t, &rested, unrested, pool)
}

func TestVisitsBeforeTheRunCountTowardsTheFirstRest(t *testing.T) {
	pool := threeHalfHours()
	p := restProblem()
	p.VisitsSinceRest = 1
	b := search(t, wide, p, pool)[0]
	if len(b.Visits) != 3 || len(b.Rests) != 1 || b.Rests[0].At != 1 {
		t.Fatalf("visits %v rests %+v", placeIDs(b), b.Rests)
	}
}

func TestRestDueBeforeTheFirstVisitOfTheRun(t *testing.T) {
	pool := threeHalfHours()
	p := restProblem()
	p.VisitsSinceRest = 2
	b := search(t, wide, p, pool)[0]
	if len(b.Visits) != 3 || len(b.Rests) == 0 || b.Rests[0].At != 0 || !b.Rests[0].StartAt.Equal(p.Start) {
		t.Fatalf("visits %v rests %+v", placeIDs(b), b.Rests)
	}
}
