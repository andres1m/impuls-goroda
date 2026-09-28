package usecase

import (
	"slices"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func stepKinds(steps []domain.Step) []string {
	var kinds []string
	for _, s := range steps {
		kind := string(s.Kind)
		if isRestStep(s) {
			kind = "rest"
		}
		kinds = append(kinds, kind)
	}
	return kinds
}

func TestRelaxedPlanShowsRestSteps(t *testing.T) {
	var pool []domain.Candidate
	for i := byte(1); i <= 6; i++ {
		pool = append(pool, place(i, genCategories[int(i)%len(genCategories)], north(origin, float64(i)*80)))
	}
	res := optimize(t, pool, estimated(), func(r *domain.OptimizeRequest) { r.Constraints.LoadProfile = "relaxed" })
	route := res.Routes[0]
	i := slices.IndexFunc(route.Steps, isRestStep)
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
		sawRest = sawRest || slices.ContainsFunc(route.Steps, isRestStep)
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
	i := slices.IndexFunc(steps, isRestStep)
	if i <= 0 {
		t.Fatalf("no rest among %d replacement steps", len(steps))
	}
	if steps[i].VisitEndAt.Sub(steps[i].VisitStartAt) != 15*time.Minute || steps[i].VisitStartAt.Before(steps[i-1].VisitEndAt) || steps[i+1].ArrivalAt.Before(steps[i].VisitEndAt) {
		t.Fatalf("rest %d of %+v", i, steps)
	}
}

// Replacements keep the rhythm of the day: a relaxed visit before the freed slot counts towards their first rest.
func TestRecomputeFillCountsVisitsBeforeTheGapTowardsRest(t *testing.T) {
	museum := place(1, domain.CategoryCulture, north(origin, 100))
	park := place(2, domain.CategoryWalk, north(origin, 200))
	relaxed := func(r *domain.OptimizeRequest) { r.Constraints.LoadProfile = "relaxed" }
	base := optimize(t, []domain.Candidate{museum, park}, estimated(), relaxed).Routes[0]
	if len(base.Steps) != 2 || slices.ContainsFunc(base.Steps, isRestStep) {
		t.Fatalf("base steps %+v", base.Steps)
	}
	catalog := []domain.Candidate{museum, park}
	for i := byte(3); i <= 7; i++ {
		catalog = append(catalog, place(i, genCategories[int(i)%(len(genCategories)-1)], north(origin, float64(i)*90)))
	}
	req := request()
	relaxed(&req)
	res := recomputeRequest(t, catalog, domain.RecomputeRequest{
		City: "perm", Timezone: "Asia/Yekaterinburg", Base: base, Constraints: req.Constraints,
		Trigger: domain.RemovalTrigger{VisitID: base.Steps[1].VisitID, Mode: domain.RemovalRebuild},
	})
	if res.Candidate == nil {
		t.Fatalf("status %s", res.Status)
	}
	kinds := stepKinds(res.Candidate.Steps)
	// The kept visit and the first replacement make two, so the rest comes right after that replacement.
	if want := []string{"visit", "visit", "rest", "visit"}; !slices.Equal(kinds, want) {
		t.Fatalf("steps %v, want %v", kinds, want)
	}
}

// Two visits before the freed slot already make a relaxed cycle, so its replacements start with a rest.
func TestRecomputeFillStartsWithADueRest(t *testing.T) {
	var kept []domain.Candidate
	for i := byte(1); i <= 3; i++ {
		kept = append(kept, place(i, genCategories[int(i)%(len(genCategories)-1)], north(origin, float64(i)*100)))
	}
	base := optimize(t, kept, estimated(), func(*domain.OptimizeRequest) {}).Routes[0]
	if len(base.Steps) != 3 {
		t.Fatalf("base has %d steps", len(base.Steps))
	}
	catalog := slices.Clone(kept)
	for i := byte(4); i <= 8; i++ {
		catalog = append(catalog, place(i, genCategories[int(i)%(len(genCategories)-1)], north(origin, float64(i)*90)))
	}
	req := request()
	req.Constraints.LoadProfile = "relaxed"
	res := recomputeRequest(t, catalog, domain.RecomputeRequest{
		City: "perm", Timezone: "Asia/Yekaterinburg", Base: base, Constraints: req.Constraints,
		Trigger: domain.RemovalTrigger{VisitID: base.Steps[2].VisitID, Mode: domain.RemovalRebuild},
	})
	if res.Candidate == nil {
		t.Fatalf("status %s", res.Status)
	}
	steps := res.Candidate.Steps
	if len(steps) < 4 || isRestStep(steps[1]) || !isRestStep(steps[2]) || steps[3].Kind != domain.StepVisit {
		t.Fatalf("steps %v, want two kept visits, a rest and replacements", stepKinds(steps))
	}
}
