package solver

import (
	"math"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func restProblem() Problem {
	p := problem()
	p.Load = LoadProfile{RestEvery: 2, Rest: 15 * time.Minute}
	return p
}

func TestRestAfterEverySecondVisit(t *testing.T) {
	categories := []domain.Category{domain.CategoryCulture, domain.CategoryWalk, domain.CategorySport, domain.CategoryTourism, domain.CategoryVolunteer}
	var pool []domain.Candidate
	for i := byte(1); i <= 5; i++ {
		pool = append(pool, place(i, categories[i-1], 0, north(origin, float64(i)*100)))
	}
	b := search(t, wide, restProblem(), pool)[0]
	if len(b.Visits) < 3 {
		t.Fatalf("only %d visits", len(b.Visits))
	}
	if len(b.Rests) == 0 || b.Rests[0].At != 2 || b.Rests[0].EndAt.Sub(b.Rests[0].StartAt) != 15*time.Minute || !b.Rests[0].StartAt.Equal(b.Visits[1].EndAt) {
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
	p.Anchors = []Anchor{anchor(concert)}
	b := search(t, wide, p, []domain.Candidate{first, second, concert})[0]
	if len(b.Visits) != 3 || b.Visits[2].Candidate.Session == nil || len(b.Rests) != 0 {
		t.Fatalf("visits %v rests %+v", placeIDs(b), b.Rests)
	}
	p.Load = LoadProfile{}
	unrested := search(t, wide, p, []domain.Candidate{first, second, concert})[0]
	if skipped := unrested.Score - b.Score; math.Abs(skipped-restSkipWeight) > 1e-9 {
		t.Fatalf("skipping the rest cost %v, want %v", skipped, float64(restSkipWeight))
	}
}

func TestNoRestsWithoutAProfile(t *testing.T) {
	var pool []domain.Candidate
	for i := byte(1); i <= 4; i++ {
		pool = append(pool, place(i, []domain.Category{domain.CategoryCulture, domain.CategoryWalk, domain.CategorySport, domain.CategoryTourism}[i-1], 0, north(origin, float64(i)*100)))
	}
	if b := search(t, wide, problem(), pool)[0]; len(b.Rests) != 0 {
		t.Fatalf("rests %+v", b.Rests)
	}
}
