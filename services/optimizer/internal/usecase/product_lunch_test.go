package usecase

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func TestRecomputeAddsExternalLunchAfterVisit(t *testing.T) {
	base := basePlan(t)
	anchor := base.Steps[0].VisitID
	id := domain.VisitID{90}
	venue := domain.ExternalVenueSnapshot{Provider: "2gis", ExternalID: "cafe-1", Title: "Cafe", Position: north(origin, 320), ObservedAt: at(9, 0), Price: domain.Price{Status: domain.PriceUnknown, Currency: "RUB"}, Availability: "unknown", HoursVerification: domain.VerificationUnknown}
	res := recompute(t, dayCatalog(), &base, domain.LunchTrigger{SchemaVersion: 1, Stops: []domain.LunchStop{{VisitID: id, AfterVisitID: anchor, Duration: 45 * time.Minute, External: &venue}}})
	if res.Status != domain.RecomputeProposed || res.Candidate == nil {
		t.Fatalf("status %s conflicts %v", res.Status, res.Conflicts)
	}
	i := slices.IndexFunc(res.Candidate.Steps, func(s domain.Step) bool { return s.VisitID == id })
	if i < 1 || res.Candidate.Steps[i].Kind != domain.StepExternalLunch || res.Candidate.Steps[i].Lunch.AfterVisitID != anchor {
		t.Fatalf("lunch placement: %+v", res.Candidate.Steps)
	}
	if res.Candidate.Legs[i].DistanceMeters == nil {
		t.Fatal("external lunch has no travel leg")
	}
	if !slices.ContainsFunc(res.Changes, func(c domain.RouteChange) bool {
		return c.Kind == domain.ChangeAdded && c.AfterVisitID != nil && *c.AfterVisitID == id
	}) {
		t.Fatalf("missing ADDED change: %+v", res.Changes)
	}
	if err := res.Candidate.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestCopyRouteRemapsExternalLunchAnchor(t *testing.T) {
	base := basePlan(t)
	venue := domain.ExternalVenueSnapshot{Provider: "2gis", ExternalID: "cafe-1", Title: "Cafe", Position: north(origin, 320), ObservedAt: at(9, 0), Price: domain.Price{Status: domain.PriceUnknown, Currency: "RUB"}, Availability: "unknown", HoursVerification: domain.VerificationUnknown}
	added := recompute(t, dayCatalog(), &base, domain.LunchTrigger{SchemaVersion: 1, Stops: []domain.LunchStop{{VisitID: domain.VisitID{90}, AfterVisitID: base.Steps[0].VisitID, Duration: 45 * time.Minute, External: &venue}}})
	if added.Candidate == nil {
		t.Fatalf("lunch proposal: %s %v", added.Status, added.Conflicts)
	}
	base = *added.Candidate
	input := domain.CopyRequest{City: "perm", Timezone: "Asia/Yekaterinburg", Base: base, Origin: base.Origin, Constraints: copyConstraints()}
	result, err := newPlanner(t, fakeSource{candidates: dayCatalog()}, estimated()).CopyRoute(context.Background(), &input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Route == nil {
		t.Fatalf("copy: %s %v", result.Status, result.Conflicts)
	}
	i := slices.IndexFunc(result.Route.Steps, func(s domain.Step) bool { return s.Kind == domain.StepExternalLunch })
	if i < 1 {
		t.Fatal("external lunch missing")
	}
	step := result.Route.Steps[i]
	if step.VisitID == (domain.VisitID{90}) || step.Lunch.AfterVisitID != result.Route.Steps[i-1].VisitID {
		t.Fatalf("copied lunch identity: %+v", step)
	}
	if err := result.Route.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSubsequentPinPreservesExternalLunch(t *testing.T) {
	base := basePlan(t)
	venue := domain.ExternalVenueSnapshot{Provider: "2gis", ExternalID: "cafe-1", Title: "Cafe", Position: north(origin, 320), ObservedAt: at(9, 0), Price: domain.Price{Status: domain.PriceUnknown, Currency: "RUB"}, Availability: "unknown", HoursVerification: domain.VerificationUnknown}
	id := domain.VisitID{90}
	added := recompute(t, dayCatalog(), &base, domain.LunchTrigger{SchemaVersion: 1, Stops: []domain.LunchStop{{VisitID: id, AfterVisitID: base.Steps[0].VisitID, Duration: 45 * time.Minute, External: &venue}}})
	if added.Candidate == nil {
		t.Fatalf("lunch proposal: %s %v", added.Status, added.Conflicts)
	}
	base = *added.Candidate
	other := base.Steps[0].VisitID
	updated := recompute(t, dayCatalog(), &base, domain.PinTrigger{VisitID: other, Kind: domain.PinPreferred})
	if updated.Candidate == nil {
		t.Fatalf("pin: %s %v", updated.Status, updated.Conflicts)
	}
	if !slices.ContainsFunc(updated.Candidate.Steps, func(s domain.Step) bool {
		return s.VisitID == id && s.Kind == domain.StepExternalLunch && s.ExternalVenue.ExternalID == venue.ExternalID
	}) {
		t.Fatal("external lunch changed after pin")
	}
}

func TestLunchTriggerKeepsLegacyAutomaticLunch(t *testing.T) {
	cafe := place(8, domain.CategoryGastro, north(origin, 150))
	optimized := optimize(t, append(city(), cafe), estimated(), withLunch)
	if len(optimized.Routes) == 0 {
		t.Fatal("no legacy lunch route")
	}
	base := optimized.Routes[0]
	_, legacy := lunchStep(t, &base)
	anchor := base.Steps[0].VisitID
	if anchor == legacy.VisitID {
		anchor = base.Steps[1].VisitID
	}
	trigger := domain.LunchTrigger{SchemaVersion: 1, Stops: []domain.LunchStop{{VisitID: domain.VisitID{90}, AfterVisitID: anchor, Duration: 45 * time.Minute}}}
	w, err := newRework(&domain.RecomputeRequest{Base: base, Trigger: trigger})
	if err != nil {
		t.Fatal(err)
	}
	w.applyLunchTrigger(trigger)
	if !slices.ContainsFunc(w.future, func(a ahead) bool { return a.step.VisitID == legacy.VisitID }) {
		t.Fatal("legacy automatic lunch removed")
	}
	if !slices.ContainsFunc(w.future, func(a ahead) bool { return a.step.VisitID == (domain.VisitID{90}) && a.step.Lunch != nil }) {
		t.Fatal("manual lunch missing")
	}
}

func TestRecomputeMovesAndRemovesLunchWithoutLosingOtherLunch(t *testing.T) {
	base := basePlan(t)
	first := domain.LunchStop{VisitID: domain.VisitID{90}, AfterVisitID: base.Steps[0].VisitID, Duration: 45 * time.Minute}
	second := domain.LunchStop{VisitID: domain.VisitID{91}, AfterVisitID: first.VisitID, Duration: 45 * time.Minute}
	added := recompute(t, dayCatalog(), &base, domain.LunchTrigger{SchemaVersion: 1, Stops: []domain.LunchStop{first, second}})
	if added.Candidate == nil {
		t.Fatalf("initial lunches: %s %v", added.Status, added.Conflicts)
	}
	base = *added.Candidate
	for i := len(base.Steps) - 1; i >= 0; i-- {
		if base.Steps[i].Lunch == nil {
			second.AfterVisitID = base.Steps[i].VisitID
			break
		}
	}
	updated := recompute(t, dayCatalog(), &base, domain.LunchTrigger{SchemaVersion: 1, Stops: []domain.LunchStop{second}})
	if updated.Candidate == nil {
		t.Fatalf("updated lunches: %s %v", updated.Status, updated.Conflicts)
	}
	if slices.ContainsFunc(updated.Candidate.Steps, func(s domain.Step) bool { return s.VisitID == first.VisitID }) {
		t.Fatal("removed lunch retained")
	}
	if !slices.ContainsFunc(updated.Candidate.Steps, func(s domain.Step) bool {
		return s.VisitID == second.VisitID && s.Lunch != nil && s.Lunch.AfterVisitID == second.AfterVisitID
	}) {
		t.Fatal("moved lunch missing")
	}
	if !slices.ContainsFunc(updated.Changes, func(c domain.RouteChange) bool {
		return c.Kind == domain.ChangeRemoved && c.BeforeVisitID != nil && *c.BeforeVisitID == first.VisitID
	}) {
		t.Fatal("removed lunch change missing")
	}
	if err := updated.Candidate.Validate(); err != nil {
		t.Fatal(err)
	}
}
