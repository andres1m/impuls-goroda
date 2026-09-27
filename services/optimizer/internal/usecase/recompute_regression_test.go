package usecase

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func recomputeRequest(t *testing.T, catalog []domain.Candidate, req domain.RecomputeRequest) domain.RecomputeResult {
	t.Helper()
	if err := req.Validate(); err != nil {
		t.Fatal(err)
	}
	res, err := newPlanner(t, fakeSource{candidates: catalog}, estimated()).Recompute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Validate(); err != nil {
		t.Fatal(err)
	}
	return res
}

func reworkRequest(base domain.Plan, trigger domain.Trigger) domain.RecomputeRequest {
	return domain.RecomputeRequest{City: "perm", Timezone: "Asia/Yekaterinburg", Base: base, Constraints: dayConstraints(), Trigger: trigger}
}

func TestRecomputeCompletedPause(t *testing.T) {
	base := basePlan(t)
	res := recompute(t, dayCatalog(), base, domain.RemovalTrigger{VisitID: base.Steps[0].VisitID, Mode: domain.RemovalFreeTime})
	base = *res.Candidate
	pause := base.Steps[0]
	base.Legs[0].Geometry = nil
	req := reworkRequest(base, domain.PinTrigger{VisitID: stepAt(t, base, 3).VisitID, Kind: domain.PinPreferred})
	req.History = []domain.VisitExecution{{VisitID: pause.VisitID, Status: domain.ExecutionCompleted, ActualStart: &pause.VisitStartAt, ActualEnd: &pause.VisitEndAt}}
	res = recomputeRequest(t, dayCatalog(), req)
	if res.Candidate == nil || res.Candidate.Steps[0].Kind != domain.StepFreeTime || res.Candidate.Steps[0].VisitID != pause.VisitID {
		t.Fatalf("pause lost: %+v", res)
	}
}

func TestRecomputePlaceObligation(t *testing.T) {
	base := basePlan(t)
	museum := stepAt(t, base, 1)
	res := recompute(t, dayCatalog(), base, domain.PinTrigger{VisitID: museum.VisitID, Kind: domain.PinObligation})
	if res.Candidate == nil || !stepAt(t, *res.Candidate, 1).Obligation {
		t.Fatal("place not pinned")
	}
	cat := dayCatalog()
	cat[0].Window.End = at(11, 0)
	res = recompute(t, cat, *res.Candidate, delay(domain.DelayAlreadyDelayed, at(12, 0)))
	if res.Status != domain.RecomputeConflict || !slices.Contains(res.Conflicts[0].VisitIDs, museum.VisitID) {
		t.Fatalf("obligation lost: %+v", res)
	}
}

func TestRecomputeCancelledPinObligation(t *testing.T) {
	base := basePlan(t)
	req := reworkRequest(base, domain.CancellationTrigger{VisitIDs: []domain.VisitID{stepAt(t, base, 3).VisitID}, MinCatalogRevision: 7})
	req.Constraints.Obligations = nil
	res := recomputeRequest(t, dayCatalog(), req)
	if res.Status != domain.RecomputeProposed || !slices.Contains(changeKinds(res.Changes), domain.ChangeParticipationAction) {
		t.Fatalf("cancellation failed: %+v", res)
	}
}

func TestRecomputeExplicitlyRemovedObligation(t *testing.T) {
	for _, mode := range []domain.RemovalMode{domain.RemovalFreeTime, domain.RemovalRebuild} {
		t.Run(string(mode), func(t *testing.T) {
			base := basePlan(t)
			concert := stepAt(t, base, 3)
			res := recompute(t, dayCatalog(), base, domain.RemovalTrigger{VisitID: concert.VisitID, Mode: mode})
			if res.Status != domain.RecomputeProposed || slices.ContainsFunc(res.Candidate.Steps, func(s domain.Step) bool { return s.VisitID == concert.VisitID }) {
				t.Fatalf("removal failed: %+v", res)
			}
		})
	}
}

func TestRecomputeSkippedObligation(t *testing.T) {
	base := basePlan(t)
	concert := stepAt(t, base, 3)
	res := recompute(t, dayCatalog(), base, delay(domain.DelayAlreadyDelayed, at(14, 40)), domain.VisitExecution{VisitID: concert.VisitID, Status: domain.ExecutionSkipped})
	if res.Status != domain.RecomputeProposed || slices.ContainsFunc(res.Candidate.Steps, func(s domain.Step) bool { return s.VisitID == concert.VisitID }) {
		t.Fatalf("skipped visit returned: %+v", res)
	}
}

func TestRecomputeSoldOutSoftSession(t *testing.T) {
	base := basePlan(t)
	base.Steps = slices.Clone(base.Steps)
	for i := range base.Steps {
		base.Steps[i].Obligation = false
	}
	req := reworkRequest(base, domain.PinTrigger{VisitID: stepAt(t, base, 1).VisitID, Kind: domain.PinPreferred})
	req.Constraints.Obligations = nil
	cat := dayCatalog()
	cat[2].Session.Availability = domain.AvailabilitySoldOut
	res := recomputeRequest(t, cat, req)
	if res.Status != domain.RecomputeProposed || slices.ContainsFunc(res.Candidate.Steps, func(s domain.Step) bool { return s.Catalog != nil && s.Catalog.PlaceID == (domain.PlaceID{3}) }) {
		t.Fatalf("sold out session retained: %+v", res)
	}
}

func TestRecomputeNoRemainingVisits(t *testing.T) {
	base := basePlan(t)
	base.Steps = slices.Clone(base.Steps)
	for i := range base.Steps {
		base.Steps[i].Obligation = false
	}
	for _, start := range []time.Time{at(17, 50), base.End, base.End.Add(time.Minute)} {
		req := reworkRequest(base, delay(domain.DelayAlreadyDelayed, start))
		req.Constraints.Obligations = nil
		res := recomputeRequest(t, dayCatalog(), req)
		if res.Status != domain.RecomputeConflict {
			t.Fatalf("start %s: want explicit refusal: %+v", start, res)
		}
	}
	for _, after := range []time.Duration{0, time.Minute} {
		res := recomputeRequest(t, dayCatalog(), reworkRequest(basePlan(t), delay(domain.DelayAlreadyDelayed, base.End.Add(after))))
		if res.Status != domain.RecomputeConflict {
			t.Fatalf("after %s: want explicit conflict: %+v", after, res)
		}
	}
}

func TestRecomputeActualHistoryOutsidePlannedTimes(t *testing.T) {
	for _, kind := range []string{"early", "short", "late"} {
		t.Run(kind, func(t *testing.T) {
			base := basePlan(t)
			first := base.Steps[0]
			start, end := base.Start.Add(-10*time.Minute), first.VisitEndAt
			switch kind {
			case "short":
				start, end = first.VisitStartAt, first.VisitStartAt.Add(time.Minute)
			case "late":
				start, end = base.End.Add(-10*time.Minute), base.End.Add(10*time.Minute)
			}
			req := reworkRequest(base, domain.PinTrigger{VisitID: stepAt(t, base, 3).VisitID, Kind: domain.PinPreferred})
			if kind == "late" {
				req.Constraints.Obligations = nil
				for i := range req.Base.Steps {
					req.Base.Steps[i].Obligation = false
				}
			}
			req.History = []domain.VisitExecution{{VisitID: first.VisitID, Status: domain.ExecutionCompleted, ActualStart: &start, ActualEnd: &end}}
			res := recomputeRequest(t, dayCatalog(), req)
			if res.Candidate == nil || !res.Candidate.Steps[0].VisitStartAt.Equal(start) || !res.Candidate.Steps[0].VisitEndAt.Equal(end) {
				t.Fatalf("history changed: %+v", res)
			}
		})
	}
}

func TestRecomputeDetectsChangedDurationAndPrice(t *testing.T) {
	for _, kind := range []string{"duration", "price"} {
		t.Run(kind, func(t *testing.T) {
			base := basePlan(t)
			cat := dayCatalog()
			if kind == "duration" {
				cat[0].Window.RecommendedDuration = cat[0].Window.MinDuration
			} else {
				v := int64(10000)
				cat[2].Offers[0].Price = domain.Price{Status: domain.PriceFixed, Currency: "RUB", LowerMinor: &v, UpperMinor: &v}
			}
			res := recompute(t, cat, base, delay(domain.DelayAlreadyDelayed, base.Start))
			if res.Status != domain.RecomputeProposed {
				t.Fatalf("changed %s ignored: %+v", kind, res)
			}
		})
	}
}

func TestRecomputeReplacementBeforePauseKeepsGeometryConnected(t *testing.T) {
	base := basePlan(t)
	paused := recompute(t, dayCatalog(), base, domain.RemovalTrigger{VisitID: base.Steps[1].VisitID, Mode: domain.RemovalFreeTime})
	res := recompute(t, append(dayCatalog(), gallery()), *paused.Candidate, domain.CancellationTrigger{VisitIDs: []domain.VisitID{base.Steps[0].VisitID}, MinCatalogRevision: 7})
	if res.Candidate == nil {
		t.Fatal("no replacement plan")
	}
	for i := 1; i < len(res.Candidate.Legs); i++ {
		a, b := res.Candidate.Legs[i-1], res.Candidate.Legs[i]
		if a.Geometry[len(a.Geometry)-1] != b.Geometry[0] {
			t.Fatalf("disconnected legs %d and %d: %v -> %v", i, i+1, a.Geometry, b.Geometry)
		}
	}
}

func TestRecomputeDelayAfterAllVisitsUpdatesDestinationLeg(t *testing.T) {
	base := optimize(t, dayCatalog(), estimated(), func(r *domain.OptimizeRequest) { r.Constraints = dayConstraints() }).Routes[0]
	resume := base.Steps[len(base.Steps)-1].DepartureAt.Add(time.Hour)
	req := reworkRequest(base, delay(domain.DelayAlreadyDelayed, resume))
	for _, s := range base.Steps {
		a, b := s.VisitStartAt, s.VisitEndAt
		req.History = append(req.History, domain.VisitExecution{VisitID: s.VisitID, Status: domain.ExecutionCompleted, ActualStart: &a, ActualEnd: &b})
	}
	res := recomputeRequest(t, dayCatalog(), req)
	if res.Candidate == nil || res.Status != domain.RecomputeProposed {
		t.Fatalf("return journey unchanged: %+v", res)
	}
	leg := res.Candidate.Legs[len(res.Candidate.Legs)-1]
	if !leg.DepartureAt.Equal(resume) || leg.Geometry[0] != origin {
		t.Fatalf("return journey starts in the past: %+v", leg)
	}
}

func TestRecomputeRepairRespectsIncreasedTicketPrice(t *testing.T) {
	base := basePlan(t)
	concert := stepAt(t, base, 3)
	req := reworkRequest(base, delay(domain.DelayAlreadyDelayed, base.Start))
	req.Constraints.Budget = domain.Budget{Mode: domain.BudgetStrict, Limit: &domain.Money{AmountMinor: 60000, Currency: "RUB"}}
	// Rebuild the base cost summary under the original strict budget.
	base = optimize(t, dayCatalog(), estimated(), func(r *domain.OptimizeRequest) { r.Constraints = req.Constraints; r.Destination = nil }).Routes[0]
	req.Base = base
	concert = stepAt(t, base, 3)
	cat := dayCatalog()
	amount := int64(70000)
	cat[2].Offers[0].Price.LowerMinor = &amount
	cat[2].Offers[0].Price.UpperMinor = &amount
	res := recomputeRequest(t, cat, req)
	if res.Status != domain.RecomputeConflict || !slices.Contains(res.Conflicts[0].VisitIDs, concert.VisitID) {
		t.Fatalf("budget conflict not explained: %+v", res)
	}
}

func TestRecomputeCompletedPinObligationSurvivesAnotherChange(t *testing.T) {
	base := basePlan(t)
	req := reworkRequest(base, domain.PinTrigger{VisitID: base.Steps[0].VisitID, Kind: domain.PinPreferred})
	req.Constraints.Obligations = nil
	concert := stepAt(t, base, 3)
	a, b := concert.VisitStartAt, concert.VisitEndAt
	req.History = []domain.VisitExecution{{VisitID: concert.VisitID, Status: domain.ExecutionCompleted, ActualStart: &a, ActualEnd: &b}}
	res := recomputeRequest(t, dayCatalog(), req)
	if res.Candidate == nil || !stepAt(t, *res.Candidate, 3).Obligation {
		t.Fatal("completed commitment lost")
	}
}

func TestRecomputeRejectsUnknownPriceUnderStrictBudget(t *testing.T) {
	cons := dayConstraints()
	cons.Obligations = nil
	cons.Budget = domain.Budget{Mode: domain.BudgetStrict, Limit: &domain.Money{AmountMinor: 60000, Currency: "RUB"}}
	base := optimize(t, dayCatalog(), estimated(), func(r *domain.OptimizeRequest) { r.Constraints = cons; r.Destination = nil }).Routes[0]
	req := reworkRequest(base, delay(domain.DelayAlreadyDelayed, base.Start))
	req.Constraints = cons
	cat := dayCatalog()
	cat[2].Offers = nil
	res := recomputeRequest(t, cat, req)
	if res.Status != domain.RecomputeConflict {
		t.Fatalf("unaffordable visit kept: %+v", res)
	}
}

func TestRecomputeBudgetIncludesCompletedVisits(t *testing.T) {
	cat := []domain.Candidate{session(1, domain.CategoryCulture, north(origin, 100), at(10, 30), at(11, 0), 40000), session(2, domain.CategoryCulture, north(origin, 200), at(12, 0), at(13, 0), 40000)}
	cons := dayConstraints()
	cons.Budget = domain.Budget{Mode: domain.BudgetStrict, Limit: &domain.Money{AmountMinor: 100000, Currency: "RUB"}}
	cons.Obligations = []domain.Obligation{{SessionID: &cat[0].Session.ID, Participation: domain.ParticipationUserReported}, {SessionID: &cat[1].Session.ID, Participation: domain.ParticipationUserReported}}
	base := optimize(t, cat, estimated(), func(r *domain.OptimizeRequest) { r.Constraints = cons; r.Destination = nil }).Routes[0]
	first := base.Steps[0]
	req := reworkRequest(base, delay(domain.DelayAlreadyDelayed, at(11, 10)))
	req.Constraints = cons
	req.History = []domain.VisitExecution{{VisitID: first.VisitID, Status: domain.ExecutionCompleted, ActualStart: &first.VisitStartAt, ActualEnd: &first.VisitEndAt}}
	amount := int64(70000)
	cat[1].Offers[0].Price.LowerMinor = &amount
	cat[1].Offers[0].Price.UpperMinor = &amount
	res := recomputeRequest(t, cat, req)
	if res.Status != domain.RecomputeConflict {
		t.Fatalf("spent budget was reused: %+v", res)
	}
}
