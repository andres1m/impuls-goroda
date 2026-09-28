package solver

import (
	"context"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func TestProfileForKnownAndUnknownNames(t *testing.T) {
	for name, hours := range map[string]float64{"relaxed": 2.5, "moderate": 1.5, "intense": 1, "": 1.5, "fast": 1.5} {
		if got := ProfileFor(name).VisitHours; got != hours {
			t.Errorf("%q: visit hours %v, want %v", name, got, hours)
		}
	}
	if ProfileFor("intense").WalkLimit != 0 || ProfileFor("relaxed").WalkLimit != 45*time.Minute || ProfileFor("moderate").WalkLimit != 90*time.Minute {
		t.Fatal("walk limits")
	}
	if r := ProfileFor("relaxed"); r.RestEvery != 2 || r.Rest != 15*time.Minute || ProfileFor("moderate").RestEvery != 0 {
		t.Fatal("rests")
	}
}

func TestLoadProfileValidate(t *testing.T) {
	if err := ProfileFor("relaxed").Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (LoadProfile{Rest: -time.Minute}).Validate(); err == nil {
		t.Fatal("negative rest accepted")
	}
	if err := (LoadProfile{OverWalkWeight: math.Inf(1)}).Validate(); err == nil {
		t.Fatal("infinite weight accepted")
	}
	p := problem()
	p.Load = LoadProfile{VisitHours: -1}
	if err := p.Validate(); err == nil {
		t.Fatal("problem with a negative visit norm accepted")
	}
}

func TestVisitNormNeverZero(t *testing.T) {
	if n := ProfileFor("relaxed").visitNorm(2 * time.Hour); n != 1 {
		t.Fatalf("two hours relaxed norm %d", n)
	}
	if n := ProfileFor("moderate").visitNorm(8 * time.Hour); n != 5 {
		t.Fatalf("eight hours moderate norm %d", n)
	}
	if n := (LoadProfile{}).visitNorm(8 * time.Hour); n != 0 {
		t.Fatalf("no profile norm %d", n)
	}
}

func TestOverWalk(t *testing.T) {
	l := ProfileFor("moderate")
	if l.overWalk(90) != 0 || l.overWalk(100) != 10*l.OverWalkWeight {
		t.Fatal("moderate walk penalty")
	}
	if ProfileFor("intense").overWalk(500) != 0 {
		t.Fatal("intense has no walk limit")
	}
}

func TestSearchKeepsVisitsNearTheNorm(t *testing.T) {
	categories := []domain.Category{domain.CategoryCulture, domain.CategoryWalk, domain.CategorySport, domain.CategoryTourism}
	var pool []domain.Candidate
	for i := byte(1); i <= 12; i++ {
		c := place(i, categories[int(i)%len(categories)], 0, north(origin, float64(i)*60))
		c.BaseScore = 1
		pool = append(pool, c)
	}
	visits := map[string]int{}
	for _, name := range []string{"relaxed", "moderate", "intense"} {
		p := problem()
		p.Load = ProfileFor(name)
		visits[name] = len(search(t, wide, p, pool)[0].Visits)
	}
	if !(visits["relaxed"] < visits["moderate"] && visits["moderate"] < visits["intense"]) || visits["relaxed"] > 4 || visits["intense"] > 8 {
		t.Fatalf("visits per profile %v", visits)
	}
}

func TestSearchDoesNotChargeAnchorsOverTheNorm(t *testing.T) {
	first := place(1, domain.CategoryCulture, 0, north(origin, 100))
	concert := session(2, domain.CategoryWalk, north(origin, 200), at(12, 0), at(13, 0))
	p := problem()
	p.End = at(13, 0)
	p.Load = LoadProfile{VisitHours: 3, OverVisitWeight: 1000}
	p.Anchors = []Anchor{anchor(concert)}
	b := search(t, greedy, p, []domain.Candidate{first, concert})[0]
	if len(b.Visits) != 2 || b.Score < 0 {
		t.Fatalf("visits %v score %v", placeIDs(b), b.Score)
	}
}

func TestWalkPenaltyCountsTheFinishOnce(t *testing.T) {
	c := place(1, domain.CategoryCulture, 0, north(origin, 1000))
	dest := north(origin, 1500)
	p := problem()
	p.Destination = &dest
	free := search(t, greedy, p, []domain.Candidate{c})[0]
	p.Load = LoadProfile{WalkLimit: time.Minute, OverWalkWeight: 1}
	charged := search(t, greedy, p, []domain.Candidate{c})[0]
	walked := free.Visits[0].Transit.Duration.Minutes() + free.Finish.Duration.Minutes()
	if penalty := free.Score - charged.Score; math.Abs(penalty-(walked-1)) > 1e-9 {
		t.Fatalf("walk penalty %v for %v walked minutes", penalty, walked)
	}
}

func TestIntenseVisitsAreShorter(t *testing.T) {
	museum := place(1, domain.CategoryCulture, 0, north(origin, 100)) // recommended an hour, at least half an hour
	length := map[string]time.Duration{}
	for _, name := range []string{"moderate", "intense"} {
		p := problem()
		p.Load = ProfileFor(name)
		v := search(t, greedy, p, []domain.Candidate{museum})[0].Visits[0]
		length[name] = v.EndAt.Sub(v.StartAt)
	}
	if length["moderate"] != time.Hour || length["intense"] != 45*time.Minute {
		t.Fatalf("visit lengths %v", length)
	}
	if museum.Window.RecommendedDuration != time.Hour {
		t.Fatal("the shared pool was modified")
	}
}

func TestRepairKeepsTrimmedVisitLengths(t *testing.T) {
	var pool []domain.Candidate
	for i := byte(1); i <= 4; i++ {
		pool = append(pool, place(i, []domain.Category{domain.CategoryCulture, domain.CategoryWalk, domain.CategorySport, domain.CategoryTourism}[i-1], 0, north(origin, float64(i)*100)))
	}
	p := problem()
	p.Load = ProfileFor("intense")
	planned := search(t, greedy, p, pool)[0]
	steps := make([]RepairStep, len(planned.Visits))
	for i, v := range planned.Visits {
		// the repair gets the catalog's candidates, not the search's trimmed copies
		steps[i] = RepairStep{Candidate: &pool[slices.IndexFunc(pool, func(c domain.Candidate) bool { return c.Place.ID == v.Candidate.Place.ID })]}
	}
	r := repair(t, p, steps...)
	if len(r.Stops) != len(planned.Visits) {
		t.Fatalf("kept %d of %d visits", len(r.Stops), len(planned.Visits))
	}
	for i, stop := range r.Stops {
		if !stop.Visit.StartAt.Equal(planned.Visits[i].StartAt) || !stop.Visit.EndAt.Equal(planned.Visits[i].EndAt) {
			t.Fatalf("visit %d moved from %s-%s to %s-%s", i, planned.Visits[i].StartAt.Format("15:04"), planned.Visits[i].EndAt.Format("15:04"), stop.Visit.StartAt.Format("15:04"), stop.Visit.EndAt.Format("15:04"))
		}
	}
}

func TestSearchRejectsMalformedCandidateAtAnyPace(t *testing.T) {
	broken := place(1, domain.CategoryCulture, 0, north(origin, 100))
	broken.Window.RecommendedDuration = broken.Window.MinDuration - time.Minute
	for _, name := range []string{"moderate", "intense"} {
		p := problem()
		p.Load = ProfileFor(name)
		if _, err := newSolver(t, greedy).Search(context.Background(), p, []domain.Candidate{broken}); err == nil {
			t.Fatalf("%s: a candidate recommended below its minimum was accepted", name)
		}
	}
}

func TestIntenseKeepsTheLengthOfACommitment(t *testing.T) {
	museum := place(1, domain.CategoryCulture, 0, north(origin, 100))
	committed := Anchor{Candidate: museum, Obligation: domain.Obligation{VisitID: &domain.VisitID{9}, Participation: domain.ParticipationUserReported}}
	p := problem()
	p.Load = ProfileFor("intense")
	p.Anchors = []Anchor{committed}
	b := search(t, greedy, p, nil)[0]
	if len(b.Visits) != 1 || b.Visits[0].EndAt.Sub(b.Visits[0].StartAt) != time.Hour {
		t.Fatalf("visits %+v", b.Visits)
	}
	r := repair(t, p, RepairStep{Candidate: &museum})
	if len(r.Stops) != 1 || r.Stops[0].Visit.EndAt.Sub(r.Stops[0].Visit.StartAt) != time.Hour {
		t.Fatalf("repair stops %+v", r.Stops)
	}
}

func TestLunchVenueIsNotChargedAgainstTheNorm(t *testing.T) {
	museum := place(1, domain.CategoryCulture, 0, north(origin, 100))
	p := lunchProblem(at(12, 0), at(14, 0))
	p.Load = LoadProfile{VisitHours: 24, OverVisitWeight: 1000}
	b := search(t, wide, p, []domain.Candidate{museum, cafe(2, north(origin, 200))})[0]
	if b.Lunch == nil || !b.Lunch.Venue || len(b.Visits) != 2 {
		t.Fatalf("visits %v lunch %+v", placeIDs(b), b.Lunch)
	}
	if b.Score < 0 {
		t.Fatalf("the lunch venue was charged as a visit over the norm: score %v", b.Score)
	}
}

func TestLunchVenueDoesNotCountTowardsTheNormOfLaterVisits(t *testing.T) {
	museum := place(1, domain.CategoryCulture, 0, north(origin, 100))
	gallery := place(3, domain.CategoryTourism, 0, north(origin, 300))
	p := lunchProblem(at(12, 0), at(16, 0))
	// Two visits in four hours: the museum and the gallery; lunch at the cafe is not one of them.
	p.Load = LoadProfile{VisitHours: 2, OverVisitWeight: 1000}
	b := search(t, wide, p, []domain.Candidate{museum, cafe(2, north(origin, 200)), gallery})[0]
	if b.Lunch == nil || !b.Lunch.Venue || len(b.Visits) != 3 || b.Score < 0 {
		t.Fatalf("visits %v lunch %+v score %v", placeIDs(b), b.Lunch, b.Score)
	}
}
