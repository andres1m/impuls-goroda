package solver

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func newSolver(t *testing.T, cfg Config) *Solver {
	t.Helper()
	s, err := New(cfg, DefaultScoreParams(), baseline(t, DefaultTransitParams()), BasicPlacement{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func search(t *testing.T, cfg Config, p Problem, pool []domain.Candidate) []*domain.Branch {
	t.Helper()
	routes, err := newSolver(t, cfg).Search(context.Background(), p, pool)
	if err != nil {
		t.Fatal(err)
	}
	return routes
}

func placeIDs(b *domain.Branch) []byte {
	ids := make([]byte, len(b.Visits))
	for i, v := range b.Visits {
		ids[i] = v.Candidate.Place.ID[0]
	}
	return ids
}

func routeKeys(routes []*domain.Branch) []string {
	keys := make([]string, len(routes))
	for i, r := range routes {
		keys[i] = fmt.Sprintf("%v %.9f", placeIDs(r), r.Score)
	}
	return keys
}

var (
	wide   = Config{BeamWidth: 16, Parallelism: 4}
	greedy = Config{BeamWidth: 1, Parallelism: 1}
)

func TestSearchEmptyPool(t *testing.T) {
	if routes := search(t, wide, problem(), nil); len(routes) != 0 {
		t.Fatalf("routes from an empty pool: %v", routeKeys(routes))
	}
}

func TestSearchSingleCandidate(t *testing.T) {
	museum := place(1, domain.CategoryCulture, 0, north(origin, 1000))
	routes := search(t, wide, problem(), []domain.Candidate{museum})
	if len(routes) != 1 || len(routes[0].Visits) != 1 {
		t.Fatalf("routes = %v", routeKeys(routes))
	}
	visit := routes[0].Visits[0]
	if !visit.ArrivalAt.Equal(at(10, 0).Add(1000*time.Second)) || !visit.StartAt.Equal(visit.ArrivalAt) || !visit.EndAt.Equal(visit.StartAt.Add(time.Hour)) {
		t.Fatalf("visit = %+v", visit)
	}
	want := 10 - 0.2*(1000.0/60) - 0.5
	if diff := routes[0].Score - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("score = %f, want %f", routes[0].Score, want)
	}
	if routes[0].Position != museum.Place.Location || !routes[0].Now.Equal(visit.EndAt) {
		t.Fatal("branch did not move to the visit")
	}
	if routes[0].CountCategory(domain.CategoryCulture) != 1 {
		t.Fatal("category not counted")
	}
}

func TestSearchPrefersInterestMatch(t *testing.T) {
	p := problem()
	p.Interests = domain.Interests(domain.InterestCinema)
	cinema := place(1, domain.CategoryCulture, domain.Interests(domain.InterestCinema), north(origin, 300))
	gym := place(2, domain.CategorySport, domain.Interests(domain.InterestStreetWorkout), north(origin, -300))
	routes := search(t, greedy, p, []domain.Candidate{gym, cinema})
	if routes[0].Visits[0].Candidate.Place.ID != cinema.Place.ID {
		t.Fatalf("first visit = %v", placeIDs(routes[0]))
	}
}

func TestSearchArchetypeBonus(t *testing.T) {
	theatre := place(1, domain.CategoryCulture, domain.Interests(domain.InterestPerformingArts), north(origin, 300))
	gallery := place(2, domain.CategoryCulture, domain.Interests(domain.InterestContemporaryArt), north(origin, -300))
	p := problem()
	for archetype, want := range map[domain.Archetype]domain.PlaceID{
		domain.ArchetypeHistoryHeritage: theatre.Place.ID,
		domain.ArchetypeUrbanAvantgarde: gallery.Place.ID,
	} {
		p.Archetype = archetype
		routes := search(t, greedy, p, []domain.Candidate{theatre, gallery})
		if routes[0].Visits[0].Candidate.Place.ID != want {
			t.Fatalf("%s: first visit = %v", archetype, placeIDs(routes[0]))
		}
	}
}

func TestSearchCategoryPenaltyBelongsToBranch(t *testing.T) {
	a := place(1, domain.CategoryCulture, 0, north(origin, 100))
	b := place(2, domain.CategoryCulture, 0, north(origin, 200))
	c := place(3, domain.CategorySport, 0, north(origin, -150))
	routes := search(t, greedy, problem(), []domain.Candidate{a, b, c})
	if got := placeIDs(routes[0]); !slices.Equal(got, []byte{1, 3, 2}) {
		t.Fatalf("greedy route = %v, want [1 3 2]", got)
	}
}

func TestSearchBestRouteMayBeShorter(t *testing.T) {
	near := place(1, domain.CategoryCulture, 0, north(origin, 100))
	far := place(2, domain.CategoryCulture, 0, north(origin, 3000))
	near.BaseScore, far.BaseScore = 1, 1
	routes := search(t, wide, problem(), []domain.Candidate{near, far})
	if got := placeIDs(routes[0]); !slices.Equal(got, []byte{1}) {
		t.Fatalf("best route = %v, want [1]", got)
	}
	if !slices.ContainsFunc(routes, func(r *domain.Branch) bool { return len(r.Visits) == 2 }) {
		t.Fatal("longer routes were not explored")
	}
}

func TestSearchIsDeterministicAcrossParallelism(t *testing.T) {
	categories := []domain.Category{domain.CategoryCulture, domain.CategorySport, domain.CategoryWalk, domain.CategoryTourism}
	var pool []domain.Candidate
	for i := range 8 {
		pool = append(pool, place(byte(i+1), categories[i%len(categories)], 0, north(origin, float64((i%4)*250-400))))
	}
	want := routeKeys(search(t, Config{BeamWidth: 4, Parallelism: 1}, problem(), pool))
	for range 5 {
		if got := routeKeys(search(t, Config{BeamWidth: 4, Parallelism: 8}, problem(), pool)); !reflect.DeepEqual(got, want) {
			t.Fatalf("parallel result %v differs from sequential %v", got, want)
		}
	}
}

func TestSearchSkipsLateSession(t *testing.T) {
	concert := session(1, domain.CategoryCulture, north(origin, 3000), at(10, 5), at(11, 0))
	museum := place(2, domain.CategoryCulture, 0, north(origin, 100))
	routes := search(t, wide, problem(), []domain.Candidate{concert, museum})
	if len(routes) == 0 {
		t.Fatal("no routes")
	}
	for _, r := range routes {
		if slices.Contains(placeIDs(r), 1) {
			t.Fatalf("unreachable session in route %v", placeIDs(r))
		}
	}
}

func TestSearchRecordsUsedSession(t *testing.T) {
	concert := session(1, domain.CategoryCulture, north(origin, 100), at(11, 0), at(12, 0))
	routes := search(t, wide, problem(), []domain.Candidate{concert})
	if len(routes) != 1 {
		t.Fatalf("routes = %v", routeKeys(routes))
	}
	if _, ok := routes[0].UsedSessions[concert.Session.ID]; !ok {
		t.Fatal("session not recorded")
	}
}

func TestSearchWithoutUsableMode(t *testing.T) {
	p := problem()
	p.Modes = []domain.MovementMode{domain.MovementCar}
	if routes := search(t, wide, p, []domain.Candidate{place(1, domain.CategoryCulture, 0, north(origin, 100))}); len(routes) != 0 {
		t.Fatalf("routes without a usable mode: %v", routeKeys(routes))
	}
}

func TestSearchResultsAreIndependent(t *testing.T) {
	pool := []domain.Candidate{
		place(1, domain.CategoryCulture, 0, north(origin, 100)),
		place(2, domain.CategorySport, 0, north(origin, 200)),
		place(3, domain.CategoryWalk, 0, north(origin, 300)),
	}
	routes := search(t, wide, problem(), pool)
	var a, b *domain.Branch
	for _, r := range routes {
		ids := placeIDs(r)
		if slices.Equal(ids, []byte{1, 2}) {
			a = r
		}
		if slices.Equal(ids, []byte{1, 2, 3}) {
			b = r
		}
	}
	if a == nil || b == nil {
		t.Fatalf("routes = %v", routeKeys(routes))
	}
	start := b.Visits[0].StartAt
	a.Visits[0].StartAt = at(17, 0)
	a.VisitedPlaces[domain.PlaceID{9}] = struct{}{}
	if !b.Visits[0].StartAt.Equal(start) {
		t.Fatal("routes share visits")
	}
	if _, ok := b.VisitedPlaces[domain.PlaceID{9}]; ok {
		t.Fatal("routes share visited places")
	}
}

func TestSearchCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	routes, err := newSolver(t, wide).Search(ctx, problem(), []domain.Candidate{place(1, domain.CategoryCulture, 0, north(origin, 100))})
	if !errors.Is(err, context.Canceled) || routes != nil {
		t.Fatalf("routes=%v err=%v", routeKeys(routes), err)
	}
}

func TestSearchRejectsInvalidInput(t *testing.T) {
	s := newSolver(t, wide)
	bad := problem()
	bad.End = bad.Start
	if _, err := s.Search(context.Background(), bad, nil); err == nil {
		t.Fatal("invalid problem accepted")
	}
	broken := place(1, domain.CategoryCulture, 0, origin)
	broken.Place.ID = domain.PlaceID{}
	pool := []domain.Candidate{place(2, domain.CategoryCulture, 0, origin), broken}
	if _, err := s.Search(context.Background(), problem(), pool); err == nil || !strings.Contains(err.Error(), "candidate 1") {
		t.Fatalf("invalid candidate: err=%v", err)
	}
}

func TestNewRejectsInvalidSetup(t *testing.T) {
	transit := baseline(t, DefaultTransitParams())
	badScore := DefaultScoreParams()
	badScore.ArchetypeBonus = 0
	cases := map[string]func() (*Solver, error){
		"config":    func() (*Solver, error) { return New(Config{}, DefaultScoreParams(), transit, BasicPlacement{}) },
		"score":     func() (*Solver, error) { return New(wide, badScore, transit, BasicPlacement{}) },
		"transit":   func() (*Solver, error) { return New(wide, DefaultScoreParams(), nil, BasicPlacement{}) },
		"placement": func() (*Solver, error) { return New(wide, DefaultScoreParams(), transit, nil) },
	}
	for name, build := range cases {
		if _, err := build(); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}
