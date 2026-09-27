package pricing

import (
	"testing"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func cost(t *testing.T, p Policy, visits ...domain.Candidate) ([]domain.CostSnapshot, domain.CostSummary) {
	t.Helper()
	refs := make([]*domain.Candidate, len(visits))
	for i := range visits {
		refs[i] = &visits[i]
	}
	snapshots, summary := p.Cost(refs)
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
