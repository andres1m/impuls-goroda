package pricing

import (
	"math"
	"testing"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func cost(t *testing.T, p Policy, visits ...domain.Candidate) ([]domain.CostSnapshot, domain.CostSummary) {
	t.Helper()
	refs := make([]*domain.Candidate, len(visits))
	for i := range visits {
		refs[i] = &visits[i]
	}
	snapshots, summary, err := p.Cost(refs)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != len(visits) {
		t.Fatalf("%d snapshots for %d visits", len(snapshots), len(visits))
	}
	for i, s := range snapshots {
		if err := s.Validate(); err != nil {
			t.Fatalf("snapshot %d: %v", i, err)
		}
	}
	if err := summary.Validate(); err != nil {
		t.Fatalf("summary: %v", err)
	}
	return snapshots, summary
}

func amount(m *domain.Money) any {
	if m == nil {
		return nil
	}
	return m.AmountMinor
}

func TestCostProgramShare(t *testing.T) {
	p := policy()
	p.Programs = []string{domain.ProgramPushkinCard}
	snapshots, summary := cost(t, p, concert(offer(1, fixed(50000), domain.AudienceGeneral, domain.ProgramPushkinCard)))
	s := snapshots[0]
	if s.PersonalAmount != nil || amount(s.ProgramAmount) != int64(50000) || len(s.UnknownComponents) != 0 {
		t.Fatalf("snapshot personal=%v program=%v unknown=%v", amount(s.PersonalAmount), amount(s.ProgramAmount), s.UnknownComponents)
	}
	if s.PriceOfferID == nil || *s.PriceOfferID != (domain.PriceOfferID{1}) || s.Audience != domain.AudienceGeneral || s.Provenance != tariff {
		t.Fatalf("snapshot offer %v audience %q provenance %+v", s.PriceOfferID, s.Audience, s.Provenance)
	}
	if summary.KnownPersonal != rub(0) || summary.ProgramAmount != rub(50000) || amount(summary.TotalUpper) != int64(50000) {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestCostProgramPaysEveryTicket(t *testing.T) {
	p := policy()
	p.Programs = []string{domain.ProgramPushkinCard}
	card := func(id byte, price int64) domain.Candidate {
		c := concert(offer(id, fixed(price), domain.AudienceGeneral, domain.ProgramPushkinCard))
		c.Place.ID = domain.PlaceID{id}
		return c
	}
	snapshots, summary := cost(t, p, card(1, 50000), card(2, 20000))
	for i, want := range []int64{50000, 20000} {
		if s := snapshots[i]; s.PersonalAmount != nil || amount(s.ProgramAmount) != want {
			t.Errorf("visit %d personal=%v program=%v", i, amount(s.PersonalAmount), amount(s.ProgramAmount))
		}
	}
	if summary.ProgramAmount != rub(70000) || summary.KnownPersonal != rub(0) {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestCostPriceKinds(t *testing.T) {
	p := policy()
	p.Budget = domain.Budget{Mode: domain.BudgetStrict, Limit: ptr(rub(100000))}
	snapshots, summary := cost(t, p,
		concert(offer(1, fixed(40000), domain.AudienceGeneral)),
		concert(offer(2, between(10000, 30000), domain.AudienceGeneral)),
		concert(offer(3, free(), domain.AudienceGeneral)),
	)
	want := []any{int64(40000), nil, int64(0)}
	for i, s := range snapshots {
		if amount(s.PersonalAmount) != want[i] || s.ProgramAmount != nil {
			t.Errorf("visit %d personal=%v program=%v", i, amount(s.PersonalAmount), amount(s.ProgramAmount))
		}
	}
	if summary.KnownPersonal != rub(40000) || summary.KnownTransport != rub(0) || summary.ProgramAmount != rub(0) {
		t.Fatalf("summary = %+v", summary)
	}
	if amount(summary.TotalLower) != int64(50000) || amount(summary.TotalUpper) != int64(70000) {
		t.Fatalf("total %v–%v", amount(summary.TotalLower), amount(summary.TotalUpper))
	}
	if summary.BudgetConclusion != domain.BudgetSatisfied {
		t.Fatalf("conclusion = %s", summary.BudgetConclusion)
	}
}

func TestCostUnknownPrice(t *testing.T) {
	p := policy()
	p.Budget = strict(1000000)
	p.AcceptUnknownPrice = true
	unpriced := concert()
	unpriced.Session.Provenance = domain.Provenance{SourceName: "sessions", FetchedAt: day}
	snapshots, summary := cost(t, p, museum(), unpriced, concert(offer(1, fixed(40000), domain.AudienceGeneral)))
	for i, s := range snapshots[:2] {
		if s.Price.Status != domain.PriceUnknown || s.PriceOfferID != nil || s.PersonalAmount != nil || s.ProgramAmount != nil ||
			len(s.UnknownComponents) != 1 || s.UnknownComponents[0].Code != "PRICE_UNKNOWN" {
			t.Errorf("visit %d snapshot = %+v", i, s)
		}
	}
	if snapshots[0].Provenance != source || snapshots[1].Provenance.SourceName != "sessions" {
		t.Fatalf("provenance %+v and %+v", snapshots[0].Provenance, snapshots[1].Provenance)
	}
	if summary.TotalLower != nil || summary.TotalUpper != nil || summary.KnownPersonal != rub(40000) {
		t.Fatalf("summary = %+v", summary)
	}
	if len(summary.UnknownComponents) != 1 || summary.BudgetConclusion != domain.BudgetUnknown {
		t.Fatalf("unknown %v conclusion %s", summary.UnknownComponents, summary.BudgetConclusion)
	}
}

func TestCostBudgetConclusion(t *testing.T) {
	visits := []domain.Candidate{
		concert(offer(1, fixed(60000), domain.AudienceGeneral)),
		concert(offer(2, between(10000, 50000), domain.AudienceGeneral)),
	}
	cases := []struct {
		name   string
		budget domain.Budget
		want   domain.BudgetConclusion
	}{
		{"no budget", domain.Budget{Mode: domain.BudgetNone}, domain.BudgetNotApplicable},
		{"upper bounds within the limit", domain.Budget{Mode: domain.BudgetAdvisory, Limit: ptr(rub(110000))}, domain.BudgetSatisfied},
		{"only lower bounds within the limit", domain.Budget{Mode: domain.BudgetAdvisory, Limit: ptr(rub(100000))}, domain.BudgetViolated},
		{"strict over the limit", strict(70000), domain.BudgetViolated},
	}
	for _, tc := range cases {
		p := policy()
		p.Budget = tc.budget
		if _, summary := cost(t, p, visits...); summary.BudgetConclusion != tc.want {
			t.Errorf("%s: conclusion = %s", tc.name, summary.BudgetConclusion)
		}
	}
}

func TestCostPricesVisitsTheSearchWouldExclude(t *testing.T) {
	p := policy()
	p.PushkinCardOnly = true
	snapshots, summary := cost(t, p, concert(offer(1, fixed(50000), domain.AudienceGeneral)), museum())
	if snapshots[0].Price.Status != domain.PriceFixed || snapshots[1].Price.Status != domain.PriceUnknown {
		t.Fatalf("snapshots = %+v", snapshots)
	}
	if summary.KnownPersonal != rub(50000) {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestCostEmptyRoute(t *testing.T) {
	p := policy()
	p.Budget = strict(100000)
	if _, summary := cost(t, p); summary.BudgetConclusion != domain.BudgetSatisfied || amount(summary.TotalUpper) != int64(0) {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestCostProgramPaysUpperOfRange(t *testing.T) {
	p := policy()
	p.Programs = []string{domain.ProgramPushkinCard}
	snapshots, summary := cost(t, p, concert(offer(1, between(10000, 30000), domain.AudienceGeneral, domain.ProgramPushkinCard)))
	if s := snapshots[0]; s.PersonalAmount != nil || amount(s.ProgramAmount) != int64(30000) {
		t.Fatalf("personal=%v program=%v", amount(s.PersonalAmount), amount(s.ProgramAmount))
	}
	if amount(summary.TotalLower) != int64(10000) || amount(summary.TotalUpper) != int64(30000) {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestCostSnapshotDoesNotShareOfferID(t *testing.T) {
	visit := concert(offer(1, fixed(50000), domain.AudienceGeneral))
	snapshots, _ := cost(t, policy(), visit)
	snapshots[0].PriceOfferID[0] = 9
	if snapshots[0].PriceOfferID == &visit.Offers[0].ID {
		t.Fatal("snapshot points into the catalog offer")
	}
}

func TestCostRejectsInvalidPolicy(t *testing.T) {
	p := policy()
	p.Budget = domain.Budget{Mode: domain.BudgetStrict}
	visit := concert(offer(1, fixed(50000), domain.AudienceGeneral))
	if _, _, err := p.Cost([]*domain.Candidate{&visit}); err == nil {
		t.Fatal("strict budget without a limit accepted")
	}
}

func TestCostRejectsOverflow(t *testing.T) {
	a := concert(offer(1, fixed(math.MaxInt64), domain.AudienceGeneral))
	b := concert(offer(2, fixed(1), domain.AudienceGeneral))
	if _, _, err := policy().Cost([]*domain.Candidate{&a, &b}); err == nil {
		t.Fatal("overflowing route cost accepted")
	}
}

func TestCostSatisfiesPlanBudgetRules(t *testing.T) {
	budgets := []domain.Budget{
		{Mode: domain.BudgetNone},
		{Mode: domain.BudgetAdvisory, Limit: ptr(rub(30000))},
		strict(100000),
	}
	routes := [][]domain.Candidate{
		{concert(offer(1, fixed(40000), domain.AudienceGeneral))},
		{museum(), concert(offer(1, fixed(40000), domain.AudienceGeneral))},
	}
	for _, budget := range budgets {
		for _, visits := range routes {
			p := policy()
			p.Budget = budget
			p.AcceptUnknownPrice = true
			_, summary := cost(t, p, visits...)
			plan := domain.Plan{Cost: summary, Steps: make([]domain.Step, len(visits))}
			if err := plan.ValidateBudget(budget); err != nil {
				t.Errorf("%s budget, %d visits: %v", budget.Mode, len(visits), err)
			}
		}
	}
}

func TestSummarizeAddsUpSnapshots(t *testing.T) {
	p := policy()
	p.Programs = []string{domain.ProgramPushkinCard}
	p.Budget = strict(200000)
	p.AcceptUnknownPrice = true
	visits := []domain.Candidate{
		concert(offer(1, fixed(40000), domain.AudienceGeneral)),
		concert(offer(2, between(10000, 30000), domain.AudienceGeneral, domain.ProgramPushkinCard)),
		museum(),
	}
	snapshots, summary := cost(t, p, visits...)
	again, err := p.Summarize(snapshots)
	if err != nil {
		t.Fatal(err)
	}
	if again.KnownPersonal != summary.KnownPersonal || again.ProgramAmount != summary.ProgramAmount ||
		(again.TotalUpper == nil) != (summary.TotalUpper == nil) || again.BudgetConclusion != summary.BudgetConclusion ||
		len(again.UnknownComponents) != len(summary.UnknownComponents) {
		t.Fatalf("summarize %+v, cost %+v", again, summary)
	}
	known, _ := cost(t, p, visits[:2]...)
	total, err := p.Summarize(known)
	if err != nil || total.TotalLower.AmountMinor != 50000 || total.TotalUpper.AmountMinor != 70000 || total.BudgetConclusion != domain.BudgetSatisfied {
		t.Fatalf("summary %+v err %v", total, err)
	}
}
