package usecase

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"

	"go.uber.org/zap"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/benchlat"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// The stand's search settings, so the numbers describe what the service actually runs.
var benchConfig = Config{Currency: "RUB", BeamWidth: 16, Parallelism: 4}

func benchPlanner(b *testing.B, pool []domain.Candidate) *Planner {
	b.Helper()
	p, err := NewPlanner(benchConfig, fakeSource{candidates: pool}, estimated(), zap.NewNop())
	if err != nil {
		b.Fatal(err)
	}
	return p
}

// benchPool packs places within about 1.5 km: with every base score at 1 a longer walk costs more
// than the next visit earns, and a sparse pool would stop each route after one visit.
func benchPool(n int) []domain.Candidate {
	pool := genPool(rand.New(rand.NewPCG(1, 0)), n)
	for i := range pool {
		l := &pool[i].Place.Location
		l.Longitude = origin.Longitude + (l.Longitude-origin.Longitude)/4
		l.Latitude = origin.Latitude + (l.Latitude-origin.Latitude)/4
	}
	return pool
}

func BenchmarkOptimize(b *testing.B) {
	for _, n := range []int{60, 200, 500} {
		b.Run(fmt.Sprintf("pool=%d", n), func(b *testing.B) {
			p, req := benchPlanner(b, benchPool(n)), request()
			var rec benchlat.Recorder
			b.ReportAllocs()
			for b.Loop() {
				rec.Time(func() {
					if _, err := p.Optimize(context.Background(), req); err != nil {
						b.Fatal(err)
					}
				})
			}
			rec.Report(b)
		})
	}
}

func BenchmarkRecompute(b *testing.B) {
	p := benchPlanner(b, benchPool(60))
	res, err := p.Optimize(context.Background(), request())
	if err != nil || len(res.Routes) == 0 {
		b.Fatalf("no base plan: %v %s", err, res.Status)
	}
	base := res.Routes[0]
	first := base.Steps[0]
	triggers := []struct {
		name    string
		trigger domain.Trigger
	}{
		{"delay", domain.DelayTrigger{Mode: domain.DelayAlreadyDelayed, EffectiveStart: first.ArrivalAt.Add(minutes(30)), Position: origin, PositionSource: domain.PositionDevice}},
		{"remove", domain.RemovalTrigger{VisitID: first.VisitID, Mode: domain.RemovalRebuild}},
	}
	for _, tc := range triggers {
		b.Run("trigger="+tc.name, func(b *testing.B) {
			req := domain.RecomputeRequest{City: "perm", Timezone: "Asia/Yekaterinburg", Base: base, Constraints: request().Constraints, Trigger: tc.trigger}
			if err := req.Validate(); err != nil {
				b.Fatal(err)
			}
			var rec benchlat.Recorder
			b.ReportAllocs()
			for b.Loop() {
				rec.Time(func() {
					if _, err := p.Recompute(context.Background(), req); err != nil {
						b.Fatal(err)
					}
				})
			}
			rec.Report(b)
		})
	}
}
