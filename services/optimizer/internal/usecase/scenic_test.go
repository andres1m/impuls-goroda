package usecase

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/solver"
)

// northScenic rates walks that end north of the origin as fully scenic.
type northScenic struct{}

func (northScenic) Score(_, to domain.Coordinate) float64 {
	if to.Latitude > origin.Latitude {
		return 1
	}
	return 0
}

type fakeScenic struct {
	err      error
	city     string
	revision domain.CatalogRevision
	calls    int
}

func (f *fakeScenic) Scenic(_ context.Context, city string, revision domain.CatalogRevision) (solver.Scenic, error) {
	f.calls++
	f.city, f.revision = city, revision
	if f.err != nil {
		return nil, f.err
	}
	return northScenic{}, nil
}

func scenicConfig() Config {
	cfg := config()
	cfg.ScenicWeights = map[string]float64{"perm": 0.1}
	return cfg
}

// oneVisit leaves room for a single visit between two equally good places south and north of the origin.
func oneVisit(r *domain.OptimizeRequest) {
	r.Destination = nil
	r.End = at(11, 15)
}

func equalPair() []domain.Candidate {
	return []domain.Candidate{
		place(1, domain.CategoryCulture, north(origin, -500)),
		place(2, domain.CategoryCulture, north(origin, 500)),
	}
}

func optimizeWith(
	t *testing.T,
	cfg Config,
	scenic ScenicProvider,
	change func(*domain.OptimizeRequest),
) domain.OptimizeResult {
	t.Helper()
	var opts []Option
	if scenic != nil {
		opts = append(opts, WithScenic(scenic))
	}
	p, err := NewPlanner(cfg, fakeSource{candidates: equalPair()}, estimated(), zap.NewNop(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	req := request()
	change(&req)
	res, err := p.Optimize(context.Background(), &req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != domain.ResultReady || len(res.Routes) == 0 {
		t.Fatalf("status %s", res.Status)
	}
	return res
}

func firstPlace(res *domain.OptimizeResult) byte {
	return res.Routes[0].Steps[0].Catalog.PlaceID[0]
}

func TestOptimizePrefersScenicWalks(t *testing.T) {
	res1 := optimizeWith(t, scenicConfig(), nil, oneVisit)
	if got := firstPlace(&res1); got != 1 {
		t.Fatalf("without scenic the tie went to place %d", got)
	}
	scenic := &fakeScenic{}
	res2 := optimizeWith(t, scenicConfig(), scenic, oneVisit)
	if got := firstPlace(&res2); got != 2 {
		t.Fatalf("scenic walk not preferred, got place %d", got)
	}
	if scenic.city != "perm" || scenic.revision != freshness.CatalogRevision {
		t.Fatalf("scenic asked for %s at revision %d", scenic.city, scenic.revision)
	}
}

func TestOptimizeWithoutScenicWeightSkipsScenic(t *testing.T) {
	scenic := &fakeScenic{}
	res := optimizeWith(t, config(), scenic, oneVisit)
	if scenic.calls != 0 || firstPlace(&res) != 1 {
		t.Fatalf("city without a weight used scenic: %d calls", scenic.calls)
	}
}

func TestOptimizeSurvivesScenicFailure(t *testing.T) {
	res := optimizeWith(t, scenicConfig(), &fakeScenic{err: errors.New("catalog down")}, oneVisit)
	if firstPlace(&res) != 1 {
		t.Fatal("failed scenic still changed the route")
	}
}

func TestNewPlannerRejectsScenicWeights(t *testing.T) {
	for _, weight := range []float64{-0.1, 0.2, 1} {
		cfg := config()
		cfg.ScenicWeights = map[string]float64{"perm": weight}
		if _, err := NewPlanner(cfg, CatalogNotReady{}, estimated(), zap.NewNop()); err == nil {
			t.Errorf("scenic weight %v accepted", weight)
		}
	}
}
