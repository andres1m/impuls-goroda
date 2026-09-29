package usecase

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

type dayStats struct {
	occupancy   []float64
	walk        []float64
	routes      int
	visits      int
	singleVisit int
	longRoutes  int
	withRest    int
}

func perRoute(s *dayStats) float64 {
	return float64(s.visits) / float64(s.routes)
}

// measure plans every generated day at the given pace and sums up how the routes use their time.
func measure(t *testing.T, profile string) dayStats {
	t.Helper()
	var s dayStats
	for seed := range uint64(200) {
		c := generate(seed)
		c.req.Constraints.LoadProfile = profile
		p, _ := plannerWithLog(t, &c, 4)
		res, err := p.Optimize(context.Background(), &c.req)
		if err != nil {
			t.Fatal(err)
		}
		for ri := range res.Routes {
			route := &res.Routes[ri]
			var busy time.Duration
			var walk float64
			visits, rested := 0, false
			for si := range route.Steps {
				st := &route.Steps[si]
				busy += st.VisitEndAt.Sub(st.VisitStartAt)
				if st.Kind == domain.StepVisit {
					visits++
				}
				rested = rested ||
					slices.ContainsFunc(
						st.AppliedConstraints,
						func(a domain.AppliedConstraint) bool { return a.Code == "REST_BREAK" },
					)
			}
			for li := range route.Legs {
				l := &route.Legs[li]
				busy += l.ArrivalAt.Sub(l.DepartureAt)
				if l.Mode == domain.MovementWalk {
					walk += l.ArrivalAt.Sub(l.DepartureAt).Minutes()
				}
			}
			s.routes++
			s.visits += visits
			s.occupancy = append(s.occupancy, busy.Minutes()/c.req.End.Sub(c.req.Start).Minutes())
			s.walk = append(s.walk, walk)
			if visits == 1 {
				s.singleVisit++
			}
			if visits >= 3 {
				s.longRoutes++
				if rested {
					s.withRest++
				}
			}
		}
	}
	return s
}

func median(v []float64) float64 {
	s := slices.Sorted(slices.Values(v))
	return s[len(s)/2]
}

func TestObjectiveFillsTheDay(t *testing.T) {
	stats := map[string]dayStats{}
	for _, profile := range []string{"relaxed", "moderate", "intense"} {
		s := measure(t, profile)
		stats[profile] = s
		t.Logf(
			"%s: routes %d, median occupancy %.2f, single-visit %.1f%%, visits/route %.2f, median walk %.0f min, rests in %d of %d routes with 3+ visits",
			profile,
			s.routes,
			median(s.occupancy),
			100*float64(s.singleVisit)/float64(s.routes),
			perRoute(&s),
			median(s.walk),
			s.withRest,
			s.longRoutes,
		)
	}
	m := stats["moderate"]
	if median(m.occupancy) < 0.7 {
		t.Errorf("moderate median occupancy %.2f, want at least 0.70", median(m.occupancy))
	}
	if float64(m.singleVisit) >= 0.1*float64(m.routes) {
		t.Errorf("moderate single-visit routes %d of %d, want under 10%%", m.singleVisit, m.routes)
	}
	relaxed, intense := stats["relaxed"], stats["intense"]
	if !(perRoute(&relaxed) < perRoute(&m) && perRoute(&m) < perRoute(&intense)) {
		t.Errorf("visits per route do not follow the pace")
	}
	if median(stats["relaxed"].walk) > 45 {
		t.Errorf("relaxed median walk %.0f min", median(stats["relaxed"].walk))
	}
	if r := stats["relaxed"]; r.longRoutes > 0 && r.withRest == 0 {
		t.Errorf("no rests in relaxed routes")
	}
}

// Pinning the first visit leaves room for every planned visit, so a brisk plan must not lose any of its shortened visits.
func TestRecomputeKeepsTheVisitLengthsOfTheLoadProfile(t *testing.T) {
	applied := 0
	for seed := range uint64(200) {
		c := generate(seed)
		c.req.Constraints.LoadProfile = "intense"
		p, _ := plannerWithLog(t, &c, 4)
		res, err := p.Optimize(context.Background(), &c.req)
		if err != nil || len(res.Routes) == 0 {
			continue
		}
		base := res.Routes[0]
		first := slices.IndexFunc(base.Steps, func(s domain.Step) bool { return s.Kind == domain.StepVisit })
		req := domain.RecomputeRequest{
			City: c.req.City, Timezone: c.req.Timezone, Base: base, Constraints: c.req.Constraints,
			Trigger: domain.PinTrigger{VisitID: base.Steps[first].VisitID, Kind: domain.PinPreferred},
		}
		if req.Validate() != nil {
			continue
		}
		out, err := p.Recompute(context.Background(), &req)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		applied++
		for _, ch := range out.Changes {
			if ch.Kind == domain.ChangeRemoved {
				t.Fatalf("seed %d: pinning the first visit made a %s change: %s", seed, ch.Kind, ch.Message)
			}
		}
	}
	if applied < 50 {
		t.Fatalf("only %d seeds recomputed", applied)
	}
}
