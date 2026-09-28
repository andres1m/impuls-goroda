package usecase

import (
	"context"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// recomputeCase replays the base plan up to a random visit as done and asks for one change there.
func recomputeCase(r *rand.Rand, c genCase, base domain.Plan) (domain.RecomputeRequest, []domain.Candidate, bool) {
	visits := slices.DeleteFunc(slices.Clone(base.Steps), func(s domain.Step) bool { return s.Kind != domain.StepVisit })
	if len(visits) == 0 {
		return domain.RecomputeRequest{}, nil, false
	}
	k := r.IntN(len(visits))
	var history []domain.VisitExecution
	for _, s := range visits[:k] {
		history = append(history, domain.VisitExecution{VisitID: s.VisitID, Status: domain.ExecutionCompleted, ActualStart: ptr(s.VisitStartAt), ActualEnd: ptr(s.VisitEndAt)})
	}
	target := visits[k]
	triggers := []domain.Trigger{
		domain.DelayTrigger{
			Mode:           []domain.DelayMode{domain.DelayAlreadyDelayed, domain.DelayFutureWait}[r.IntN(2)],
			EffectiveStart: target.ArrivalAt.Add(minutes(5 + r.IntN(86))), Position: c.req.Origin, PositionSource: domain.PositionDevice,
		},
		domain.RemovalTrigger{VisitID: target.VisitID, Mode: []domain.RemovalMode{domain.RemovalRebuild, domain.RemovalFreeTime}[r.IntN(2)]},
		domain.PinTrigger{VisitID: target.VisitID, Kind: domain.PinPreferred},
	}
	cancelled := slices.Clone(c.pool)
	if target.Catalog != nil && target.Catalog.SessionID != nil {
		for i := range cancelled {
			if cancelled[i].Session != nil && cancelled[i].Session.ID == *target.Catalog.SessionID {
				session := *cancelled[i].Session
				session.Availability = domain.AvailabilityCancelled
				cancelled[i].Session = &session
			}
		}
		triggers = append(triggers, domain.CancellationTrigger{VisitIDs: []domain.VisitID{target.VisitID}, MinCatalogRevision: freshness.CatalogRevision})
	}
	first := r.IntN(len(triggers))
	for i := range triggers {
		trigger := triggers[(first+i)%len(triggers)]
		req := domain.RecomputeRequest{City: c.req.City, Timezone: c.req.Timezone, Base: base, Constraints: c.req.Constraints, History: history, Trigger: trigger}
		if req.Validate() != nil {
			continue
		}
		if _, cancel := trigger.(domain.CancellationTrigger); cancel {
			return req, cancelled, true
		}
		return req, c.pool, true
	}
	return domain.RecomputeRequest{}, nil, false
}

// recomputeOnce reports whether the seed had nothing to recompute.
func recomputeOnce(t *testing.T, seed uint64) bool {
	t.Helper()
	c := generate(seed)
	p, _ := plannerWithLog(t, c, 4)
	res, err := p.Optimize(context.Background(), c.req)
	if err != nil || len(res.Routes) == 0 {
		return true
	}
	req, pool, ok := recomputeCase(rand.New(rand.NewPCG(seed, 1)), c, res.Routes[0])
	if !ok {
		return true
	}
	c.pool = pool
	p, logs := plannerWithLog(t, c, 4)
	out, err := p.Recompute(context.Background(), req)
	if err != nil {
		t.Fatalf("recompute %T: %v", req.Trigger, err)
	}
	if err := out.Validate(); err != nil {
		t.Fatalf("result: %v", err)
	}
	if n := rejections(logs); n > 0 {
		t.Fatalf("validator rejected %d plans", n)
	}
	if v := checkRecompute(req, pool, out); len(v) > 0 {
		t.Fatalf("%T: %s", req.Trigger, describe(v))
	}
	return false
}

func FuzzRecompute(f *testing.F) {
	for seed := range uint64(200) {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, seed uint64) {
		if recomputeOnce(t, seed) {
			t.Skip("the seed has no route to recompute")
		}
	})
}

// Seeds without a route have nothing to recompute; the rest must still leave a real sample.
func TestRecomputeSeedsMostlyApply(t *testing.T) {
	applied := 0
	for seed := range uint64(200) {
		if !recomputeOnce(t, seed) {
			applied++
		}
	}
	t.Logf("%d of 200 seeds recomputed", applied)
	if applied < 80 {
		t.Fatalf("only %d of 200 seeds recomputed", applied)
	}
}
