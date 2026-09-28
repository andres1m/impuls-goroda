package solver

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/benchlat"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

var benchCategories = []domain.Category{domain.CategoryCulture, domain.CategorySport, domain.CategoryWalk, domain.CategoryTourism}

// benchPool spreads places along a 6 km line; the fixed seed keeps runs comparable.
func benchPool(n int) []domain.Candidate {
	r := rand.New(rand.NewPCG(1, 0))
	pool := make([]domain.Candidate, n)
	for i := range pool {
		c := place(1, benchCategories[r.IntN(len(benchCategories))], domain.InterestMask(r.Uint64()&0x1FFF), north(origin, (r.Float64()-0.5)*6000))
		c.Place.ID = domain.PlaceID{0xB0, byte(i >> 8), byte(i)}
		pool[i] = c
	}
	return pool
}

func benchSolver(b *testing.B) *Solver {
	b.Helper()
	transit, err := NewBaselineTransit(DefaultTransitParams())
	if err != nil {
		b.Fatal(err)
	}
	s, err := New(Config{BeamWidth: 16, Parallelism: 4}, DefaultScoreParams(), transit, WindowPlacement{})
	if err != nil {
		b.Fatal(err)
	}
	return s
}

func BenchmarkSearch(b *testing.B) {
	s := benchSolver(b)
	for _, n := range []int{60, 200, 500} {
		b.Run(fmt.Sprintf("pool=%d", n), func(b *testing.B) {
			pool := benchPool(n)
			var rec benchlat.Recorder
			b.ReportAllocs()
			for b.Loop() {
				rec.Time(func() {
					if _, err := s.Search(context.Background(), problem(), pool); err != nil {
						b.Fatal(err)
					}
				})
			}
			rec.Report(b)
		})
	}
}

func BenchmarkRepair(b *testing.B) {
	s := benchSolver(b)
	branches, err := s.Search(context.Background(), problem(), benchPool(60))
	if err != nil || len(branches) == 0 {
		b.Fatalf("no route to repair: %v", err)
	}
	steps := make([]RepairStep, 0, len(branches[0].Visits))
	for _, v := range branches[0].Visits {
		steps = append(steps, RepairStep{Candidate: v.Candidate})
	}
	delayed := problem()
	delayed.Start = delayed.Start.Add(45 * time.Minute)
	var rec benchlat.Recorder
	b.ReportAllocs()
	for b.Loop() {
		rec.Time(func() {
			if _, err := s.Repair(context.Background(), delayed, steps); err != nil {
				b.Fatal(err)
			}
		})
	}
	rec.Report(b)
}
