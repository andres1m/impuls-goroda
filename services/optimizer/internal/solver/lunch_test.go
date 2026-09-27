package solver

import (
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func TestLunchSlotFor(t *testing.T) {
	for _, tc := range []struct {
		name       string
		start, end time.Time
		min        time.Duration
		want       time.Duration
	}{
		{"short request grows to the minimum", at(13, 0), at(14, 30), 30 * time.Minute, 45 * time.Minute},
		{"request inside the range is kept", at(13, 0), at(14, 30), 50 * time.Minute, 50 * time.Minute},
		{"long request is capped", at(13, 0), at(14, 30), 90 * time.Minute, time.Hour},
		{"short window is used whole", at(13, 0), at(13, 40), 30 * time.Minute, 40 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slot := LunchSlotFor(domain.LunchWindow{Start: tc.start, End: tc.end, MinDuration: tc.min})
			if slot.Duration != tc.want || !slot.Start.Equal(tc.start) || !slot.End.Equal(tc.end) {
				t.Fatalf("slot = %+v, want duration %v", slot, tc.want)
			}
		})
	}
}

func lunchProblem(start, end time.Time) Problem {
	p := problem()
	p.Start, p.End = start, end
	p.Lunch = &LunchSlot{Start: at(13, 0), End: at(14, 30), Duration: 45 * time.Minute}
	return p
}

func cafe(id byte, location domain.Coordinate) domain.Candidate {
	return place(id, domain.CategoryGastro, domain.InterestMask(1<<7), location)
}

// requireLunch checks every route reserves lunch inside the window for exactly its duration.
func requireLunch(t *testing.T, p Problem, routes []*domain.Branch) {
	t.Helper()
	if len(routes) == 0 {
		t.Fatal("no routes")
	}
	for _, r := range routes {
		l := r.Lunch
		if l == nil {
			t.Fatalf("route %v has no lunch", placeIDs(r))
		}
		if l.StartAt.Before(p.Lunch.Start) || l.EndAt.After(p.Lunch.End) || l.EndAt.Sub(l.StartAt) != p.Lunch.Duration {
			t.Fatalf("lunch %+v does not fit %+v", l, p.Lunch)
		}
		if l.Venue {
			v := r.Visits[l.At]
			if !v.StartAt.Equal(l.StartAt) || !v.EndAt.Equal(l.EndAt) || v.Candidate.Category() != domain.CategoryGastro {
				t.Fatalf("venue lunch %+v does not match its visit %+v", l, v)
			}
			continue
		}
		if l.At > len(r.Visits) ||
			(l.At > 0 && r.Visits[l.At-1].EndAt.After(l.StartAt)) ||
			(l.At < len(r.Visits) && r.Visits[l.At].ArrivalAt.Before(l.EndAt)) {
			t.Fatalf("pause %+v overlaps the visits of %v", l, placeIDs(r))
		}
	}
}

func TestSearchLunchAtVenueOfNearestRing(t *testing.T) {
	near := cafe(1, north(origin, 250))
	far := cafe(2, north(origin, 700))
	far.BaseScore = 100
	p := lunchProblem(at(13, 0), at(14, 0))
	routes := search(t, wide, p, []domain.Candidate{near, far})
	requireLunch(t, p, routes)
	for _, r := range routes {
		if !r.Lunch.Venue || r.Visits[r.Lunch.At].Candidate.Place.ID != near.Place.ID {
			t.Fatalf("route %v does not lunch at the nearest venue", placeIDs(r))
		}
	}
}

func TestSearchLunchVenueFromWiderRing(t *testing.T) {
	venue := cafe(1, north(origin, 900))
	p := lunchProblem(at(13, 0), at(14, 30))
	routes := search(t, wide, p, []domain.Candidate{venue})
	requireLunch(t, p, routes)
	if !routes[0].Lunch.Venue {
		t.Fatalf("lunch %+v is not at the venue within 1000 m", routes[0].Lunch)
	}
}

func TestSearchFreeLunch(t *testing.T) {
	museum := place(9, domain.CategoryCulture, 0, north(origin, 400))
	closed := cafe(1, north(origin, 200))
	closed.Window.End = at(12, 0)
	distant := cafe(2, north(origin, 1500))
	long := cafe(3, north(origin, 100))
	long.Window.MinDuration = 90 * time.Minute
	long.Window.RecommendedDuration = 2 * time.Hour
	for _, tc := range []struct {
		name string
		pool []domain.Candidate
	}{
		{"no venue", []domain.Candidate{museum}},
		{"venue closed at lunch", []domain.Candidate{museum, closed}},
		{"venue beyond the widest ring", []domain.Candidate{museum, distant}},
		{"venue needs longer than lunch", []domain.Candidate{museum, long}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := lunchProblem(at(10, 0), at(18, 0))
			routes := search(t, wide, p, tc.pool)
			requireLunch(t, p, routes)
			for _, r := range routes {
				if r.Lunch.Venue {
					t.Fatalf("route %v lunches at a venue", placeIDs(r))
				}
			}
		})
	}
}

func TestSearchFreeLunchCountsNoCategory(t *testing.T) {
	museum := place(9, domain.CategoryCulture, 0, north(origin, 400))
	p := lunchProblem(at(12, 0), at(15, 0))
	routes := search(t, greedy, p, []domain.Candidate{museum})
	requireLunch(t, p, routes)
	if routes[0].CountCategory(domain.CategoryGastro) != 0 || routes[0].CountCategory(domain.CategoryCulture) != 1 {
		t.Fatalf("category counts = %v", routes[0].CategoryCounts)
	}
}

func TestSearchWithoutRoomForLunchFindsNothing(t *testing.T) {
	concert := session(5, domain.CategoryCulture, north(origin, 300), at(12, 30), at(15, 0))
	p := lunchProblem(at(12, 0), at(18, 0))
	p.Anchors = []Anchor{{Candidate: concert}}
	if routes := search(t, wide, p, []domain.Candidate{cafe(1, north(origin, 200))}); len(routes) != 0 {
		t.Fatalf("routes without lunch: %v", routeKeys(routes))
	}
}

func TestSearchLunchBetweenAnchors(t *testing.T) {
	morning := session(5, domain.CategoryCulture, north(origin, 300), at(11, 0), at(13, 0))
	evening := session(6, domain.CategorySport, north(origin, 600), at(14, 0), at(16, 0))
	p := lunchProblem(at(10, 0), at(18, 0))
	p.Anchors = []Anchor{{Candidate: morning}, {Candidate: evening}}
	routes := search(t, wide, p, []domain.Candidate{cafe(1, north(origin, 450))})
	requireLunch(t, p, routes)
	for _, r := range routes {
		if len(r.Visits) < 2 || r.Lunch.StartAt.Before(at(13, 0)) || r.Lunch.EndAt.After(at(14, 0)) {
			t.Fatalf("route %v lunch %+v is not between the anchors", placeIDs(r), r.Lunch)
		}
	}
}

func TestSearchFreeLunchKeepsDestinationReachable(t *testing.T) {
	museum := place(9, domain.CategoryCulture, 0, north(origin, 400))
	p := lunchProblem(at(12, 0), at(15, 0))
	destination := north(origin, 3000)
	p.Destination = &destination
	routes := search(t, wide, p, []domain.Candidate{museum})
	requireLunch(t, p, routes)
	for _, r := range routes {
		if r.Finish == nil || r.Now.Add(r.Finish.Duration).After(p.End) {
			t.Fatalf("route %v does not reach the destination in time", placeIDs(r))
		}
	}
}

func TestSearchPauseAloneIsNoRoute(t *testing.T) {
	p := lunchProblem(at(13, 0), at(14, 0))
	if routes := search(t, wide, p, nil); len(routes) != 0 {
		t.Fatalf("routes of lunch alone: %v", routeKeys(routes))
	}
}

func TestProblemRejectsInvalidLunch(t *testing.T) {
	p := lunchProblem(at(10, 0), at(18, 0))
	p.Lunch.Duration = 3 * time.Hour
	if err := p.Validate(); err == nil {
		t.Fatal("lunch longer than its window accepted")
	}
}

func TestSearchDropsBranchesWithoutTimeForLunch(t *testing.T) {
	museum := place(9, domain.CategoryCulture, 0, north(origin, 200))
	museum.Window.RecommendedDuration = 3 * time.Hour
	museum.BaseScore = 100
	p := lunchProblem(at(12, 0), at(18, 0))
	routes := search(t, greedy, p, []domain.Candidate{museum, cafe(1, north(origin, 250))})
	requireLunch(t, p, routes)
}

func TestSearchVisitsLunchVenuesOnlyForLunch(t *testing.T) {
	museum := place(9, domain.CategoryCulture, 0, north(origin, 400))
	venue := cafe(1, north(origin, 200))
	venue.BaseScore = 100
	p := lunchProblem(at(10, 0), at(18, 0))
	routes := search(t, wide, p, []domain.Candidate{museum, venue})
	requireLunch(t, p, routes)
	for _, r := range routes {
		for i, v := range r.Visits {
			if v.Candidate.Place.ID == venue.Place.ID && (!r.Lunch.Venue || r.Lunch.At != i) {
				t.Fatalf("route %v visits the venue outside lunch", placeIDs(r))
			}
		}
	}
}
