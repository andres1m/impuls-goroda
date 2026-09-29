package usecase

import (
	"context"
	"testing"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func TestCopyRouteRecomputesOrderedVisitsWithoutAuthorState(t *testing.T) {
	base := basePlan(t)
	firstID := base.Steps[0].VisitID
	firstParticipation := base.Steps[0].Participation
	previous := make(map[domain.VisitID]bool)
	for _, step := range base.Steps {
		previous[step.VisitID] = true
	}
	input := domain.CopyRequest{City: "perm", Timezone: "Asia/Yekaterinburg", Base: base,
		Origin: north(origin, 100), Constraints: copyConstraints()}
	result, err := newPlanner(t, fakeSource{candidates: dayCatalog()}, estimated()).CopyRoute(context.Background(), &input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Route == nil || result.Status != domain.ResultReady {
		t.Fatalf("copy refused: %+v", result)
	}
	if len(result.Route.Steps) != len(base.Steps) {
		t.Fatalf("visits changed: %+v", result.Route.Steps)
	}
	if result.Route.Origin != input.Origin {
		t.Fatalf("origin was not changed: %+v", result.Route.Origin)
	}
	for i, step := range result.Route.Steps {
		if previous[step.VisitID] {
			t.Fatalf("author visit id reused: %v", step.VisitID)
		}
		if step.Catalog.PlaceID != base.Steps[i].Catalog.PlaceID {
			t.Fatalf("visit order changed at %d", i)
		}
		if step.Participation.Evidence != domain.EvidenceNone {
			t.Fatalf("author participation copied: %+v", step.Participation)
		}
		if step.Obligation || step.Pinned {
			t.Fatalf("author commitment copied: %+v", step)
		}
		for _, c := range step.AppliedConstraints {
			if c.Code == obligationReachable {
				t.Fatalf("synthetic copy obligation leaked: %+v", c)
			}
		}
	}
	for _, warning := range result.Route.Warnings {
		if warning.Code == "UNVERIFIED_TRANSITION" {
			t.Fatalf("synthetic copy obligation warning leaked: %+v", warning)
		}
	}
	if base.Steps[0].VisitID != firstID || base.Steps[0].Participation != firstParticipation {
		t.Fatal("source plan was mutated")
	}
}

func TestCopyRouteUsesRecipientBudgetMode(t *testing.T) {
	base := basePlan(t)
	constraints := copyConstraints()
	constraints.Budget.Mode = domain.BudgetStrict
	constraints.AcceptedUnknowns = []string{domain.AcceptUnknownPrice}
	limit := domain.Money{Currency: base.Cost.KnownPersonal.Currency, AmountMinor: 100000}
	constraints.Budget.Limit = &limit
	input := domain.CopyRequest{City: "perm", Timezone: "Asia/Yekaterinburg", Base: base,
		Origin: base.Origin, Destination: base.Destination, Constraints: constraints}
	result, err := newPlanner(t, fakeSource{candidates: dayCatalog()}, estimated()).CopyRoute(context.Background(), &input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Route == nil || result.Status != domain.ResultPartial {
		t.Fatalf("recipient budget rejected copy: %+v", result)
	}
}

func TestCopyRouteKeepsRecipientCommitment(t *testing.T) {
	base := basePlan(t)
	input := domain.CopyRequest{City: "perm", Timezone: "Asia/Yekaterinburg", Base: base,
		Origin: base.Origin, Constraints: dayConstraints()}
	result, err := newPlanner(t, fakeSource{candidates: dayCatalog()}, estimated()).CopyRoute(context.Background(), &input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Route == nil {
		t.Fatalf("recipient commitment rejected: %+v", result)
	}
	for _, step := range result.Route.Steps {
		if step.Catalog.SessionID != nil && *step.Catalog.SessionID == (domain.SessionID{3}) {
			if !step.Obligation {
				t.Fatalf("recipient commitment lost: %+v", step)
			}
		} else if step.Obligation {
			t.Fatalf("other visit became a commitment: %+v", step)
		}
	}
}

func copyConstraints() domain.RouteConstraints {
	constraints := dayConstraints()
	constraints.Obligations = nil
	return constraints
}

func TestCopyRouteSameOriginStillReturnsNewRoute(t *testing.T) {
	base := basePlan(t)
	input := domain.CopyRequest{City: "perm", Timezone: "Asia/Yekaterinburg", Base: base,
		Origin: base.Origin, Destination: base.Destination, Constraints: dayConstraints()}
	result, err := newPlanner(t, fakeSource{candidates: dayCatalog()}, estimated()).CopyRoute(context.Background(), &input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Route == nil || result.Route.Steps[0].VisitID == base.Steps[0].VisitID {
		t.Fatalf("copy did not create a new route: %+v", result)
	}
}

func TestCopyRouteMissingVisitRefusesWithoutDraft(t *testing.T) {
	base := basePlan(t)
	input := domain.CopyRequest{City: "perm", Timezone: "Asia/Yekaterinburg", Base: base,
		Origin: base.Origin, Destination: base.Destination, Constraints: dayConstraints()}
	result, err := newPlanner(t, fakeSource{candidates: nil}, estimated()).CopyRoute(context.Background(), &input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != domain.ResultConflict || result.Route != nil || len(result.Conflicts) == 0 {
		t.Fatalf("missing visit was not refused: %+v", result)
	}
}

func TestCopyRouteChangedEventAtSamePlaceRefusesWithoutDraft(t *testing.T) {
	base := basePlan(t)
	for i := range base.Steps {
		if base.Steps[i].Catalog.SessionID == nil {
			base.Steps[i].Catalog.EventID = &domain.EventID{99}
			break
		}
	}
	input := domain.CopyRequest{City: "perm", Timezone: "Asia/Yekaterinburg", Base: base,
		Origin: base.Origin, Constraints: copyConstraints()}
	result, err := newPlanner(t, fakeSource{candidates: dayCatalog()}, estimated()).CopyRoute(context.Background(), &input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != domain.ResultNoFeasibleRoute || result.Route != nil {
		t.Fatalf("different event was accepted as the same visit: %+v", result)
	}
}
