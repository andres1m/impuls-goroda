package solver

import (
	"context"
	"math"
	"testing"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// northScenic rates walks that end north of the origin as fully scenic and all others as bare.
type northScenic struct{}

func (northScenic) Score(_, to domain.Coordinate) float64 {
	if to.Latitude > origin.Latitude {
		return 1
	}
	return 0
}

type flatScenic float64

func (s flatScenic) Score(_, _ domain.Coordinate) float64 { return float64(s) }

func scenicSolver(t *testing.T, cfg Config, weight float64) *Solver {
	t.Helper()
	params := DefaultScoreParams()
	params.ScenicWeight = weight
	s, err := New(cfg, params, baseline(t, DefaultTransitParams()), WindowPlacement{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func scenicSearch(t *testing.T, weight float64, p Problem, pool []domain.Candidate) []*domain.Branch {
	t.Helper()
	routes, err := scenicSolver(t, wide, weight).Search(context.Background(), p, pool)
	if err != nil {
		t.Fatal(err)
	}
	return routes
}

func TestScoreParamsKeepScenicBelowTransitCost(t *testing.T) {
	for _, weight := range []float64{-0.1, 0.2, 0.3, math.Inf(1), math.NaN()} {
		p := DefaultScoreParams()
		p.ScenicWeight = weight
		if err := p.Validate(); err == nil {
			t.Errorf("scenic weight %v accepted", weight)
		}
	}
	p := DefaultScoreParams()
	p.TransitWeight, p.ScenicWeight = 0, 0
	if err := p.Validate(); err != nil {
		t.Fatalf("zero weights rejected: %v", err)
	}
}

// oneVisitDay leaves room for a single visit.
func oneVisitDay() Problem {
	p := problem()
	p.End = at(11, 15)
	return p
}

func TestSearchPrefersScenicWalk(t *testing.T) {
	south := place(1, domain.CategoryCulture, 0, north(origin, -500))
	north := place(2, domain.CategoryCulture, 0, north(origin, 500))
	pool := []domain.Candidate{south, north}
	p := oneVisitDay()
	if plain := scenicSearch(t, 0.1, p, pool); plain[0].Visits[0].Candidate.Place.ID != south.Place.ID {
		t.Fatalf("without scenic the tie goes to %v", placeIDs(plain[0]))
	}
	p.Scenic = northScenic{}
	routes := scenicSearch(t, 0.1, p, pool)
	if routes[0].Visits[0].Candidate.Place.ID != north.Place.ID {
		t.Fatalf("scenic walk not preferred: %v", routeKeys(routes))
	}
}

func TestScenicLowersWalkCostOnly(t *testing.T) {
	museum := place(1, domain.CategoryCulture, 0, north(origin, 1000))
	p := oneVisitDay()
	p.Scenic = flatScenic(1)
	routes := scenicSearch(t, 0.1, p, []domain.Candidate{museum})
	minutes := 1000 * 1.25 / 75
	want := worth(routes[0].Visits[0]) - 0.2*minutes + 0.1*minutes - 5
	if math.Abs(routes[0].Score-want) > 1e-9 {
		t.Fatalf("score = %f, want %f", routes[0].Score, want)
	}

	far := place(1, domain.CategoryCulture, 0, north(origin, 3000))
	p.End = at(12, 0)
	withScenic := scenicSearch(t, 0.1, p, []domain.Candidate{far})
	p.Scenic = nil
	without := scenicSearch(t, 0.1, p, []domain.Candidate{far})
	if withScenic[0].Visits[0].Transit.Mode != domain.MovementTransit || withScenic[0].Score != without[0].Score {
		t.Fatalf("a ride earns a scenic bonus: %f vs %f", withScenic[0].Score, without[0].Score)
	}
}

func TestScenicCountsWalkToDestination(t *testing.T) {
	museum := place(1, domain.CategoryCulture, 0, north(origin, 1000))
	destination := north(origin, 2000)
	p := problem()
	p.Destination = &destination
	p.Scenic = flatScenic(0.5)
	routes := scenicSearch(t, 0.1, p, []domain.Candidate{museum})
	leg := 1000 * 1.25 / 75
	want := worth(routes[0].Visits[0]) - 2*(0.2*leg-0.1*0.5*leg) - 5
	if math.Abs(routes[0].Score-want) > 1e-9 {
		t.Fatalf("score = %f, want %f", routes[0].Score, want)
	}
}

func TestScenicClampsOutOfRangeScores(t *testing.T) {
	museum := place(1, domain.CategoryCulture, 0, north(origin, 1000))
	p := oneVisitDay()
	p.Scenic = flatScenic(5)
	routes := scenicSearch(t, 0.19, p, []domain.Candidate{museum})
	if routes[0].Score >= worth(routes[0].Visits[0])-5 {
		t.Fatalf("scenic made a walk free: score %f", routes[0].Score)
	}
}

func TestScenicKeepsAnchors(t *testing.T) {
	scenicDetour := place(1, domain.CategoryCulture, 0, north(origin, 1500))
	scenicDetour.BaseScore = 1
	ticket := session(5, domain.CategorySport, north(origin, -1200), at(11, 0), at(12, 0))
	p := problem()
	p.Scenic = northScenic{}
	p.Anchors = []Anchor{{Candidate: ticket}}
	routes := scenicSearch(t, 0.19, p, []domain.Candidate{scenicDetour})
	if len(routes) == 0 {
		t.Fatal("no routes")
	}
	for _, r := range routes {
		i := len(r.Visits)
		for j, v := range r.Visits {
			if v.Candidate.Session != nil && v.Candidate.Session.ID == ticket.Session.ID {
				i = j
			}
		}
		if i == len(r.Visits) || !r.Visits[i].StartAt.Equal(at(11, 0)) {
			t.Fatalf("route %v drops or moves the anchor", placeIDs(r))
		}
	}
}
