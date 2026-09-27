package usecase

import (
	"context"
	"errors"
	"slices"
	"testing"

	"go.uber.org/zap"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func newPlanner(t *testing.T, src CandidateSource, provider TransitProvider) *Planner {
	t.Helper()
	p, err := NewPlanner(config(), src, provider, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func optimize(t *testing.T, candidates []domain.Candidate, provider TransitProvider, change func(*domain.OptimizeRequest)) domain.OptimizeResult {
	t.Helper()
	req := request()
	if change != nil {
		change(&req)
	}
	res, err := newPlanner(t, fakeSource{candidates: candidates}, provider).Optimize(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Validate(); err != nil {
		t.Fatalf("invalid result: %v", err)
	}
	for i, route := range res.Routes {
		if err := route.ValidateBudget(req.Constraints.Budget); err != nil {
			t.Fatalf("route %d budget: %v", i, err)
		}
	}
	return res
}

func city() []domain.Candidate {
	return []domain.Candidate{
		place(1, domain.CategoryCulture, north(origin, 300)),
		place(2, domain.CategoryWalk, north(origin, -400)),
		session(3, domain.CategoryCulture, north(origin, 600), at(13, 0), at(14, 30), 50000),
	}
}

func warningCodes(warnings []domain.Warning) []string {
	codes := make([]string, len(warnings))
	for i, w := range warnings {
		codes[i] = w.Code
	}
	return codes
}

func conflictCodes(conflicts []domain.Conflict) []string {
	codes := make([]string, len(conflicts))
	for i, c := range conflicts {
		codes[i] = c.Code
	}
	return codes
}

func TestOptimizeBuildsReadyRoutes(t *testing.T) {
	provider := estimated()
	res := optimize(t, city(), provider, nil)
	if res.Status != domain.ResultReady || len(res.Routes) == 0 || len(res.Routes) > 3 {
		t.Fatalf("status %s with %d routes", res.Status, len(res.Routes))
	}
	if res.Data != freshness || res.ComputationTime <= 0 {
		t.Fatalf("data %+v time %s", res.Data, res.ComputationTime)
	}
	for _, route := range res.Routes {
		if route.Result != domain.ResultReady || route.CatalogRevision != 7 || len(route.Legs) != len(route.Steps)+1 {
			t.Fatalf("route %+v", route)
		}
		for _, step := range route.Steps {
			if step.Obligation || step.Catalog == nil || step.Cost == nil {
				t.Fatalf("step %+v", step)
			}
		}
	}
	if !slices.Contains(provider.points, origin) || !slices.Contains(provider.points, destination) || len(provider.points) != 5 {
		t.Fatalf("routing points %v", provider.points)
	}
}

func TestOptimizeHonoursObligation(t *testing.T) {
	res := optimize(t, city(), estimated(), func(r *domain.OptimizeRequest) {
		r.Constraints.Obligations = []domain.Obligation{{SessionID: &domain.SessionID{3}, Participation: domain.ParticipationUserReported}}
	})
	if res.Status != domain.ResultReady || len(res.Routes) == 0 {
		t.Fatalf("status %s", res.Status)
	}
	for _, route := range res.Routes {
		i := slices.IndexFunc(route.Steps, func(s domain.Step) bool {
			return s.Catalog.SessionID != nil && *s.Catalog.SessionID == domain.SessionID{3}
		})
		if i < 0 {
			t.Fatal("obligation missing")
		}
		step := route.Steps[i]
		if !step.Obligation || step.Participation.Status != domain.ParticipationUserReported || step.Participation.Evidence != domain.EvidenceUser {
			t.Fatalf("obligation step %+v", step)
		}
		if !slices.ContainsFunc(step.AppliedConstraints, func(c domain.AppliedConstraint) bool { return c.Outcome == domain.OutcomeConditional }) {
			t.Fatalf("reachability not marked conditional: %+v", step.AppliedConstraints)
		}
		if !slices.ContainsFunc(route.Warnings, func(w domain.Warning) bool {
			return w.Code == "UNVERIFIED_TRANSITION" && w.VisitID != nil && *w.VisitID == step.VisitID
		}) {
			t.Fatalf("warnings %v", warningCodes(route.Warnings))
		}
	}
}

func TestOptimizeReportsObligationConflicts(t *testing.T) {
	overlap := session(4, domain.CategorySport, north(origin, -600), at(13, 30), at(14, 30), 10000)
	cases := map[string]struct {
		obligations []domain.Obligation
		code        string
	}{
		"overlapping obligations": {[]domain.Obligation{
			{SessionID: &domain.SessionID{3}, Participation: domain.ParticipationUserReported},
			{SessionID: &domain.SessionID{4}, Participation: domain.ParticipationUserReported},
		}, "OBLIGATIONS_OVERLAP"},
		"session not in the catalog": {[]domain.Obligation{
			{SessionID: &domain.SessionID{42}, Participation: domain.ParticipationUserReported},
		}, "OBLIGATION_UNAVAILABLE"},
	}
	for name, tc := range cases {
		res := optimize(t, append(city(), overlap), estimated(), func(r *domain.OptimizeRequest) { r.Constraints.Obligations = tc.obligations })
		if res.Status != domain.ResultConflict || len(res.Routes) != 0 || !slices.Contains(conflictCodes(res.Conflicts), tc.code) {
			t.Errorf("%s: status %s conflicts %v", name, res.Status, conflictCodes(res.Conflicts))
		}
	}
}

func TestOptimizeWithoutCandidates(t *testing.T) {
	res := optimize(t, nil, estimated(), nil)
	if res.Status != domain.ResultNoFeasibleRoute || !slices.Contains(warningCodes(res.Warnings), "NO_FEASIBLE_ROUTE") {
		t.Fatalf("status %s warnings %v", res.Status, warningCodes(res.Warnings))
	}
	unreachable := session(3, domain.CategoryCulture, north(origin, 600), at(19, 0), at(20, 0), 50000)
	res = optimize(t, []domain.Candidate{unreachable}, estimated(), nil)
	if res.Status != domain.ResultNoFeasibleRoute || !slices.Contains(warningCodes(res.Warnings), "NO_FEASIBLE_ROUTE") {
		t.Fatalf("status %s warnings %v", res.Status, warningCodes(res.Warnings))
	}
}

func TestOptimizePartialOnAcceptedUnknownPrice(t *testing.T) {
	res := optimize(t, []domain.Candidate{place(1, domain.CategoryCulture, north(origin, 300))}, estimated(), func(r *domain.OptimizeRequest) {
		r.Constraints.Budget = domain.Budget{Mode: domain.BudgetStrict, Limit: &domain.Money{AmountMinor: 100000, Currency: "RUB"}}
		r.Constraints.AcceptedUnknowns = []string{domain.AcceptUnknownPrice}
	})
	if res.Status != domain.ResultPartial || len(res.Routes) != 1 || res.Routes[0].Result != domain.ResultPartial {
		t.Fatalf("status %s routes %d", res.Status, len(res.Routes))
	}
}

func TestOptimizeWarnsWhenRoutingDegrades(t *testing.T) {
	res := optimize(t, city(), &fakeProvider{transit: baseline(), degraded: true}, nil)
	if res.Status != domain.ResultReady || !slices.Contains(warningCodes(res.Warnings), "ROUTING_DEGRADED") {
		t.Fatalf("status %s warnings %v", res.Status, warningCodes(res.Warnings))
	}
	for _, route := range res.Routes {
		for _, leg := range route.Legs {
			if leg.Verification != domain.VerificationUnknown {
				t.Fatalf("leg %+v", leg)
			}
		}
	}
}

func TestOptimizeDropsPlansTheValidatorRejects(t *testing.T) {
	overclaiming := &fakeProvider{transit: statusTransit{base: baseline(), status: domain.VerificationVerified}}
	if res := optimize(t, city(), overclaiming, nil); res.Status != domain.ResultNoFeasibleRoute || len(res.Routes) != 0 {
		t.Fatalf("status %s with %d routes", res.Status, len(res.Routes))
	}
}

func TestOptimizeCollapsesIdenticalRoutes(t *testing.T) {
	untagged := place(1, domain.CategoryWalk, north(origin, 100))
	h1 := place(2, domain.CategoryCulture, north(origin, 300))
	h1.Place.InterestMask = domain.Interests(domain.InterestClassicalArt)
	h2 := place(3, domain.CategoryTourism, north(origin, 350))
	h2.Place.InterestMask = domain.Interests(domain.InterestExcursions)
	res := optimize(t, []domain.Candidate{untagged, h1, h2}, estimated(), nil)
	if len(res.Routes) != 1 || res.Routes[0].Archetype != domain.ArchetypeHistoryHeritage {
		t.Fatalf("routes %+v", res.Routes)
	}
	if !slices.Contains(warningCodes(res.Warnings), "FEWER_ARCHETYPES") {
		t.Fatalf("warnings %v", warningCodes(res.Warnings))
	}
}

func TestOptimizeContrastingArchetypes(t *testing.T) {
	avantgarde := place(1, domain.CategoryCulture, north(origin, 300))
	avantgarde.Place.InterestMask = domain.Interests(domain.InterestContemporaryArt)
	heritage := place(2, domain.CategoryTourism, north(origin, 350))
	heritage.Place.InterestMask = domain.Interests(domain.InterestClassicalArt)
	social := place(3, domain.CategorySport, north(origin, 400))
	social.Place.InterestMask = domain.Interests(domain.InterestScienceTech)

	res := optimize(t, []domain.Candidate{avantgarde, heritage, social}, estimated(), func(r *domain.OptimizeRequest) {
		r.Constraints.InterestMask = domain.Interests(domain.InterestContemporaryArt)
	})
	if len(res.Routes) != 3 || slices.Contains(warningCodes(res.Warnings), "FEWER_ARCHETYPES") {
		t.Fatalf("routes %d warnings %v", len(res.Routes), warningCodes(res.Warnings))
	}
	wantPlace := map[domain.Archetype]domain.PlaceID{
		domain.ArchetypeUrbanAvantgarde: {1},
		domain.ArchetypeHistoryHeritage: {2},
		domain.ArchetypeActionSocial:    {3},
	}
	for i, route := range res.Routes {
		if route.Archetype != archetypes[i] {
			t.Fatalf("route %d archetype %s, want %s", i, route.Archetype, archetypes[i])
		}
		target := wantPlace[route.Archetype]
		if !slices.ContainsFunc(route.Steps, func(s domain.Step) bool {
			return s.Catalog.PlaceID == target && hasSoftConstraint(s.AppliedConstraints, "ARCHETYPE_MATCH")
		}) {
			t.Fatalf("route %s steps %+v", route.Archetype, route.Steps)
		}
		for _, s := range route.Steps {
			gotInterest := hasSoftConstraint(s.AppliedConstraints, "INTEREST_MATCH")
			if wantInterest := s.Catalog.PlaceID == (domain.PlaceID{1}); gotInterest != wantInterest {
				t.Fatalf("step at %v applied constraints %+v", s.Catalog.PlaceID, s.AppliedConstraints)
			}
		}
	}

	// When a high-value candidate carries tags of two archetypes, the colliding archetype falls back
	// to a branch that introduces its own unvisited place.
	shared := place(4, domain.CategoryCulture, north(origin, 250))
	shared.Place.InterestMask = domain.Interests(domain.InterestContemporaryArt, domain.InterestClassicalArt)
	southHeritage := place(5, domain.CategoryTourism, north(origin, -300))
	southHeritage.Place.InterestMask = domain.Interests(domain.InterestClassicalArt)
	res = optimize(t, []domain.Candidate{shared, southHeritage}, estimated(), nil)
	if len(res.Routes) != 2 || res.Routes[0].Archetype != domain.ArchetypeUrbanAvantgarde || res.Routes[1].Archetype != domain.ArchetypeHistoryHeritage {
		t.Fatalf("routes %+v", res.Routes)
	}
	if !slices.Contains(warningCodes(res.Warnings), "FEWER_ARCHETYPES") {
		t.Fatalf("warnings %v", warningCodes(res.Warnings))
	}
}

func hasSoftConstraint(cs []domain.AppliedConstraint, code string) bool {
	return slices.ContainsFunc(cs, func(c domain.AppliedConstraint) bool {
		return c.Code == code && c.Strength == domain.StrengthSoft && c.Outcome == domain.OutcomeSatisfied
	})
}

func TestOptimizeWarnsAboutTravelCosts(t *testing.T) {
	far := session(5, domain.CategoryCulture, north(origin, 4000), at(15, 0), at(16, 0), 20000)
	res := optimize(t, []domain.Candidate{far}, estimated(), func(r *domain.OptimizeRequest) { r.Destination = nil })
	if len(res.Routes) != 1 || !slices.Contains(warningCodes(res.Routes[0].Warnings), "TRANSPORT_COST_NOT_INCLUDED") {
		t.Fatalf("routes %d", len(res.Routes))
	}
	leg := res.Routes[0].Legs[0]
	if leg.Mode != domain.MovementTransit || leg.Cost.Price.Status != domain.PriceUnknown || len(leg.Cost.UnknownComponents) == 0 {
		t.Fatalf("leg %+v", leg)
	}
}

func TestOptimizePassesSourceErrors(t *testing.T) {
	p := newPlanner(t, CatalogNotReady{}, estimated())
	if _, err := p.Optimize(context.Background(), request()); !errors.Is(err, ErrCatalogNotReady) {
		t.Fatalf("err = %v", err)
	}
}

func TestOptimizeRejectsBudgetInAnotherCurrency(t *testing.T) {
	req := request()
	req.Constraints.Budget = domain.Budget{Mode: domain.BudgetStrict, Limit: &domain.Money{AmountMinor: 1000, Currency: "EUR"}}
	_, err := newPlanner(t, fakeSource{candidates: city()}, estimated()).Optimize(context.Background(), req)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
}

func TestNewPlannerRejectsInvalidSetup(t *testing.T) {
	bad := config()
	bad.BeamWidth = 0
	if _, err := NewPlanner(bad, CatalogNotReady{}, estimated(), zap.NewNop()); err == nil {
		t.Fatal("invalid search config accepted")
	}
	if _, err := NewPlanner(config(), nil, estimated(), zap.NewNop()); err == nil {
		t.Fatal("missing source accepted")
	}
}
