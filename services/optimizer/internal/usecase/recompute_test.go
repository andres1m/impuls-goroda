package usecase

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// day plan: a museum and a park around a committed concert, with a gallery left in the catalog.
func dayCatalog() []domain.Candidate {
	museum := place(1, domain.CategoryCulture, north(origin, 300))
	park := place(2, domain.CategoryWalk, north(origin, -400))
	concert := session(3, domain.CategoryCulture, north(origin, 600), at(13, 0), at(14, 30), 50000)
	for _, c := range []*domain.Candidate{&museum, &park, &concert} {
		c.BaseScore = 10
	}
	return []domain.Candidate{museum, park, concert}
}

func gallery() domain.Candidate {
	g := place(4, domain.CategoryCulture, north(origin, 350))
	g.BaseScore = 10
	return g
}

var concertObligation = domain.Obligation{SessionID: &domain.SessionID{3}, Participation: domain.ParticipationUserReported}

func dayConstraints() domain.RouteConstraints {
	c := request().Constraints
	c.Obligations = []domain.Obligation{concertObligation}
	return c
}

// basePlan is a real plan of the day, the way the planner built it.
func basePlan(t *testing.T) domain.Plan {
	t.Helper()
	res := optimize(t, dayCatalog(), estimated(), func(r *domain.OptimizeRequest) {
		r.Constraints = dayConstraints()
		r.Destination = nil
	})
	if res.Status != domain.ResultReady {
		t.Fatalf("base plan status %s", res.Status)
	}
	for _, route := range res.Routes {
		if len(route.Steps) >= 3 {
			return route
		}
	}
	t.Fatalf("no base plan with three visits among %d routes", len(res.Routes))
	return domain.Plan{}
}

func stepAt(t *testing.T, plan domain.Plan, placeID byte) domain.Step {
	t.Helper()
	i := slices.IndexFunc(plan.Steps, func(s domain.Step) bool { return s.Catalog != nil && s.Catalog.PlaceID == domain.PlaceID{placeID} })
	if i < 0 {
		t.Fatalf("plan has no visit to place %d", placeID)
	}
	return plan.Steps[i]
}

func recompute(t *testing.T, catalog []domain.Candidate, base domain.Plan, trigger domain.Trigger, history ...domain.VisitExecution) domain.RecomputeResult {
	t.Helper()
	req := domain.RecomputeRequest{
		City: "perm", Timezone: "Asia/Yekaterinburg", Base: base, Constraints: dayConstraints(), History: history, Trigger: trigger,
	}
	if err := req.Validate(); err != nil {
		t.Fatalf("invalid request: %v", err)
	}
	res, err := newPlanner(t, fakeSource{candidates: catalog}, estimated()).Recompute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Validate(); err != nil {
		t.Fatalf("invalid result: %v", err)
	}
	if res.Candidate != nil {
		if err := res.Candidate.ValidateBudget(req.Constraints.Budget); err != nil {
			t.Fatalf("candidate budget: %v", err)
		}
	}
	return res
}

func delay(mode domain.DelayMode, start time.Time) domain.DelayTrigger {
	return domain.DelayTrigger{Mode: mode, EffectiveStart: start, Position: origin, PositionSource: domain.PositionDevice}
}

func changeKinds(changes []domain.RouteChange) []domain.ChangeKind {
	kinds := make([]domain.ChangeKind, len(changes))
	for i, c := range changes {
		kinds[i] = c.Kind
	}
	return kinds
}

func schedule(p *domain.Plan) []string {
	var out []string
	for _, s := range p.Steps {
		out = append(out, s.VisitStartAt.Format("15:04:05")+"-"+s.VisitEndAt.Format("15:04:05"))
	}
	return out
}

func TestRecomputeDelayCountsTheDelayOnce(t *testing.T) {
	base := basePlan(t)
	resume := base.Start.Add(40 * time.Minute)
	late := recompute(t, dayCatalog(), base, delay(domain.DelayAlreadyDelayed, resume))
	waiting := recompute(t, dayCatalog(), base, delay(domain.DelayFutureWait, resume))
	if late.Status != domain.RecomputeProposed || late.Candidate == nil {
		t.Fatalf("status %s conflicts %v", late.Status, late.Conflicts)
	}
	if !slices.Equal(schedule(late.Candidate), schedule(waiting.Candidate)) {
		t.Fatalf("delay modes disagree: %v vs %v", schedule(late.Candidate), schedule(waiting.Candidate))
	}
	if first := late.Candidate.Steps[0]; first.VisitStartAt.Before(resume) {
		t.Fatalf("first visit starts at %s before the user resumes", first.VisitStartAt)
	}
	if departure := late.Candidate.Legs[0].DepartureAt; !departure.Equal(resume) {
		t.Fatalf("the user sets off at %s, not when they resume at %s", departure, resume)
	}
	if !slices.Contains(changeKinds(late.Changes), domain.ChangeTimeShifted) {
		t.Fatalf("changes %v", changeKinds(late.Changes))
	}
	concert := stepAt(t, base, 3)
	kept := stepAt(t, *late.Candidate, 3)
	if kept.VisitID != concert.VisitID || !kept.Obligation {
		t.Fatalf("committed concert changed identity or mark: %+v", kept)
	}
}

func TestRecomputeRefusesToDropAnUnreachableObligation(t *testing.T) {
	base := basePlan(t)
	res := recompute(t, dayCatalog(), base, delay(domain.DelayAlreadyDelayed, at(13, 5)))
	concert := stepAt(t, base, 3)
	if res.Status != domain.RecomputeConflict || res.Candidate != nil || len(res.Conflicts) != 1 ||
		res.Conflicts[0].Code != "OBLIGATION_UNREACHABLE" || !slices.Contains(res.Conflicts[0].VisitIDs, concert.VisitID) {
		t.Fatalf("status %s conflicts %+v", res.Status, res.Conflicts)
	}
}

func TestRecomputeKeepsHistoryAndDropsSkippedVisits(t *testing.T) {
	base := basePlan(t)
	first, second := base.Steps[0], base.Steps[1]
	actualStart, actualEnd := first.VisitStartAt.Add(5*time.Minute), first.VisitEndAt.Add(20*time.Minute)
	history := []domain.VisitExecution{
		{VisitID: first.VisitID, Status: domain.ExecutionCompleted, ActualStart: &actualStart, ActualEnd: &actualEnd},
	}
	if second.Obligation {
		t.Skip("fixture plan starts with the obligation")
	}
	history = append(history, domain.VisitExecution{VisitID: second.VisitID, Status: domain.ExecutionSkipped})
	res := recompute(t, dayCatalog(), base, domain.PinTrigger{VisitID: stepAt(t, base, 3).VisitID, Kind: domain.PinObligation}, history...)
	if res.Status != domain.RecomputeProposed {
		t.Fatalf("status %s conflicts %v", res.Status, res.Conflicts)
	}
	done := res.Candidate.Steps[0]
	if done.VisitID != first.VisitID || !done.VisitStartAt.Equal(actualStart) || !done.VisitEndAt.Equal(actualEnd) {
		t.Fatalf("history rewritten: %+v", done)
	}
	if slices.ContainsFunc(res.Candidate.Steps, func(s domain.Step) bool { return s.VisitID == second.VisitID }) {
		t.Fatal("skipped visit is still planned")
	}
	if !slices.ContainsFunc(res.Changes, func(c domain.RouteChange) bool {
		return c.Kind == domain.ChangeRemoved && c.BeforeVisitID != nil && *c.BeforeVisitID == second.VisitID
	}) {
		t.Fatalf("changes %v", changeKinds(res.Changes))
	}
}

func TestRecomputeReplacesACancelledVisit(t *testing.T) {
	base := basePlan(t)
	museum := stepAt(t, base, 1)
	res := recompute(t, append(dayCatalog(), gallery()), base, domain.CancellationTrigger{VisitIDs: []domain.VisitID{museum.VisitID}, MinCatalogRevision: 7})
	if res.Status != domain.RecomputeProposed {
		t.Fatalf("status %s conflicts %v", res.Status, res.Conflicts)
	}
	stepAt(t, *res.Candidate, 4)
	if slices.ContainsFunc(res.Candidate.Steps, func(s domain.Step) bool { return s.VisitID == museum.VisitID }) {
		t.Fatal("cancelled visit is still planned")
	}
	if !slices.ContainsFunc(res.Changes, func(c domain.RouteChange) bool {
		return c.Kind == domain.ChangeReplaced && *c.BeforeVisitID == museum.VisitID
	}) {
		t.Fatalf("changes %v", changeKinds(res.Changes))
	}
}

func TestRecomputeNeverDropsACancelledObligationSilently(t *testing.T) {
	base := basePlan(t)
	concert := stepAt(t, base, 3)
	res := recompute(t, dayCatalog(), base, domain.CancellationTrigger{VisitIDs: []domain.VisitID{concert.VisitID}, MinCatalogRevision: 7})
	if res.Status != domain.RecomputeProposed {
		t.Fatalf("status %s conflicts %v", res.Status, res.Conflicts)
	}
	kinds := changeKinds(res.Changes)
	if !slices.Contains(kinds, domain.ChangeParticipationAction) {
		t.Fatalf("no action about the ticket: %v", kinds)
	}
}

func TestRecomputeRemovalKeepsAPause(t *testing.T) {
	base := basePlan(t)
	museum := stepAt(t, base, 1)
	res := recompute(t, append(dayCatalog(), gallery()), base, domain.RemovalTrigger{VisitID: museum.VisitID, Mode: domain.RemovalFreeTime})
	if res.Status != domain.RecomputeProposed {
		t.Fatalf("status %s conflicts %v", res.Status, res.Conflicts)
	}
	i := slices.IndexFunc(res.Candidate.Steps, func(s domain.Step) bool { return s.Kind == domain.StepFreeTime })
	if i < 0 {
		t.Fatal("no pause in place of the removed visit")
	}
	pause := res.Candidate.Steps[i]
	if !pause.VisitStartAt.Equal(museum.VisitStartAt) || !pause.VisitEndAt.Equal(museum.VisitEndAt) {
		t.Fatalf("pause %s–%s, visit was %s–%s", pause.VisitStartAt, pause.VisitEndAt, museum.VisitStartAt, museum.VisitEndAt)
	}
	for _, step := range base.Steps {
		if step.VisitID == museum.VisitID {
			continue
		}
		kept := stepAt(t, *res.Candidate, step.Catalog.PlaceID[0])
		if !kept.VisitStartAt.Equal(step.VisitStartAt) {
			t.Fatalf("visit to %d moved from %s to %s", step.Catalog.PlaceID[0], step.VisitStartAt, kept.VisitStartAt)
		}
	}
}

func TestRecomputeRemovalRebuildsTheGap(t *testing.T) {
	base := basePlan(t)
	museum := stepAt(t, base, 1)
	res := recompute(t, append(dayCatalog(), gallery()), base, domain.RemovalTrigger{VisitID: museum.VisitID, Mode: domain.RemovalRebuild})
	if res.Status != domain.RecomputeProposed {
		t.Fatalf("status %s conflicts %v", res.Status, res.Conflicts)
	}
	stepAt(t, *res.Candidate, 4)
}

func TestRecomputePinMarksTheVisit(t *testing.T) {
	base := basePlan(t)
	museum := stepAt(t, base, 1)
	res := recompute(t, dayCatalog(), base, domain.PinTrigger{VisitID: museum.VisitID, Kind: domain.PinPreferred})
	if res.Status != domain.RecomputeProposed || !stepAt(t, *res.Candidate, 1).Pinned {
		t.Fatalf("status %s", res.Status)
	}
	if !slices.ContainsFunc(res.Changes, func(c domain.RouteChange) bool {
		return c.Kind == domain.ChangeKept && *c.BeforeVisitID == museum.VisitID
	}) {
		t.Fatalf("changes %v", changeKinds(res.Changes))
	}
}

func TestRecomputeWithoutChangesIsUnchanged(t *testing.T) {
	base := basePlan(t)
	res := recompute(t, dayCatalog(), base, delay(domain.DelayAlreadyDelayed, base.Start))
	if res.Status != domain.RecomputeUnchanged || res.Candidate != nil {
		t.Fatalf("status %s changes %v", res.Status, changeKinds(res.Changes))
	}
}

func TestRecomputeNeedsAFreshCatalog(t *testing.T) {
	base := basePlan(t)
	req := domain.RecomputeRequest{
		City: "perm", Timezone: "Asia/Yekaterinburg", Base: base, Constraints: dayConstraints(),
		Trigger: domain.CancellationTrigger{VisitIDs: []domain.VisitID{base.Steps[0].VisitID}, MinCatalogRevision: 8},
	}
	if _, err := newPlanner(t, fakeSource{candidates: dayCatalog()}, estimated()).Recompute(context.Background(), req); !errors.Is(err, ErrStaleCatalog) {
		t.Fatalf("err = %v", err)
	}
	if _, err := newPlanner(t, CatalogNotReady{}, estimated()).Recompute(context.Background(), req); !errors.Is(err, ErrCatalogNotReady) {
		t.Fatalf("err = %v", err)
	}
}
