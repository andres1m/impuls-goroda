package usecase

import (
	"slices"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func isRest(s domain.Step) bool {
	return s.Kind == domain.StepFreeTime && slices.ContainsFunc(s.AppliedConstraints, func(c domain.AppliedConstraint) bool { return c.Code == "REST_BREAK" })
}

func TestRelaxedPlanShowsRestSteps(t *testing.T) {
	var pool []domain.Candidate
	for i := byte(1); i <= 6; i++ {
		pool = append(pool, place(i, genCategories[int(i)%len(genCategories)], north(origin, float64(i)*80)))
	}
	res := optimize(t, pool, estimated(), func(r *domain.OptimizeRequest) { r.Constraints.LoadProfile = "relaxed" })
	route := res.Routes[0]
	i := slices.IndexFunc(route.Steps, isRest)
	if i < 0 {
		t.Fatalf("no rest step in %d steps", len(route.Steps))
	}
	if route.Steps[i].VisitEndAt.Sub(route.Steps[i].VisitStartAt) != 15*time.Minute {
		t.Fatal("rest length")
	}
}

func TestRestAndLunchPauseKeepTimeOrder(t *testing.T) {
	var pool []domain.Candidate
	for i := byte(1); i <= 5; i++ {
		pool = append(pool, place(i, genCategories[int(i)%(len(genCategories)-1)], north(origin, float64(i)*80)))
	}
	res := optimize(t, pool, estimated(), func(r *domain.OptimizeRequest) {
		r.Constraints.LoadProfile = "relaxed"
		r.Constraints.LunchWindow = &domain.LunchWindow{Start: at(12, 0), End: at(15, 0), MinDuration: 45 * time.Minute}
	})
	sawRest := false
	for _, route := range res.Routes {
		for i := 1; i < len(route.Steps); i++ {
			if route.Steps[i].VisitStartAt.Before(route.Steps[i-1].VisitEndAt) {
				t.Fatalf("step %d starts before step %d ends", i+1, i)
			}
		}
		sawRest = sawRest || slices.ContainsFunc(route.Steps, isRest)
	}
	if !sawRest {
		t.Fatal("no route rests, so the order check proved nothing")
	}
}

func TestOptimizePaceFollowsLoadProfile(t *testing.T) {
	pool := benchPool(60)
	visits := map[string]int{}
	for _, name := range []string{"relaxed", "moderate", "intense"} {
		res := optimize(t, pool, estimated(), func(r *domain.OptimizeRequest) { r.Constraints.LoadProfile = name })
		for _, route := range res.Routes {
			visits[name] += len(slices.DeleteFunc(slices.Clone(route.Steps), func(s domain.Step) bool { return s.Kind != domain.StepVisit }))
		}
	}
	if !(visits["relaxed"] < visits["moderate"] && visits["moderate"] < visits["intense"]) {
		t.Fatalf("visits per profile %v", visits)
	}
}

// A relaxed replacement of three or more visits rests like any relaxed route does.
func TestRecomputeFillKeepsRests(t *testing.T) {
	museum := place(1, domain.CategoryCulture, north(origin, 100))
	relaxed := func(r *domain.OptimizeRequest) { r.Constraints.LoadProfile = "relaxed" }
	base := optimize(t, []domain.Candidate{museum}, estimated(), relaxed).Routes[0]
	// With the only visit gone, the whole day is free for replacements.
	catalog := []domain.Candidate{museum}
	for i := byte(2); i <= 6; i++ {
		catalog = append(catalog, place(i, genCategories[int(i)%(len(genCategories)-1)], north(origin, float64(i)*90)))
	}
	req := request()
	relaxed(&req)
	res := recomputeRequest(t, catalog, domain.RecomputeRequest{
		City: "perm", Timezone: "Asia/Yekaterinburg", Base: base, Constraints: req.Constraints,
		Trigger: domain.RemovalTrigger{VisitID: base.Steps[0].VisitID, Mode: domain.RemovalRebuild},
	})
	if res.Candidate == nil {
		t.Fatalf("status %s", res.Status)
	}
	steps := res.Candidate.Steps
	i := slices.IndexFunc(steps, isRest)
	if i <= 0 {
		t.Fatalf("no rest among %d replacement steps", len(steps))
	}
	if steps[i].VisitEndAt.Sub(steps[i].VisitStartAt) != 15*time.Minute || steps[i].VisitStartAt.Before(steps[i-1].VisitEndAt) || steps[i+1].ArrivalAt.Before(steps[i].VisitEndAt) {
		t.Fatalf("rest %d of %+v", i, steps)
	}
}
