package pricing

import (
	"testing"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func TestPolicyFor(t *testing.T) {
	c := domain.RouteConstraints{
		Budget:           strict(300000),
		BenefitPrograms:  []string{domain.ProgramPushkinCard},
		ProgramBalance:   &domain.ProgramBalance{Program: domain.ProgramPushkinCard, Balance: rub(100000)},
		AudienceClaims:   []string{"student"},
		AcceptedUnknowns: []string{"weather", domain.AcceptUnknownPrice},
		PushkinCardOnly:  true,
	}
	p := PolicyFor("RUB", c)
	if p.Currency != "RUB" || p.Budget.Mode != domain.BudgetStrict || p.Balance != c.ProgramBalance ||
		len(p.Programs) != 1 || len(p.Audiences) != 1 || !p.PushkinCardOnly || !p.AcceptUnknownPrice {
		t.Fatalf("policy = %+v", p)
	}
	c.AcceptedUnknowns = []string{"weather"}
	if PolicyFor("RUB", c).AcceptUnknownPrice {
		t.Fatal("unknown price accepted without the user's consent")
	}
}

func TestPolicyValidate(t *testing.T) {
	if err := policy().Validate(); err != nil {
		t.Fatalf("valid policy: %v", err)
	}
	cases := map[string]func(*Policy){
		"bad currency":   func(p *Policy) { p.Currency = "rub" },
		"invalid budget": func(p *Policy) { p.Budget = domain.Budget{Mode: domain.BudgetStrict} },
		"budget in other currency": func(p *Policy) {
			p.Budget = domain.Budget{Mode: domain.BudgetStrict, Limit: &domain.Money{AmountMinor: 1, Currency: "EUR"}}
		},
		"balance in other currency": func(p *Policy) {
			p.Balance = &domain.ProgramBalance{Program: domain.ProgramPushkinCard, Balance: domain.Money{Currency: "EUR"}}
		},
		"invalid balance": func(p *Policy) { p.Balance = &domain.ProgramBalance{Balance: rub(1)} },
	}
	for name, change := range cases {
		p := policy()
		change(&p)
		if p.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestQuote(t *testing.T) {
	withPrograms := func(p *Policy) { p.Programs = []string{"sport_card", domain.ProgramPushkinCard} }
	withStudent := func(p *Policy) { p.Audiences = []string{"student"} }
	withStrict := func(p *Policy) { p.Budget = strict(100000) }
	accepting := func(p *Policy) { p.Budget = strict(100000); p.AcceptUnknownPrice = true }
	pushkinOnly := func(p *Policy) { p.PushkinCardOnly = true }
	pushkinAccepting := func(p *Policy) { p.PushkinCardOnly = true; p.AcceptUnknownPrice = true }
	none := func(*Policy) {}

	ageLimited := offer(2, fixed(30000), domain.AudienceGeneral)
	ageLimited.EligibilityAgeMax = ptr(22)
	euro := offer(2, fixed(3000), domain.AudienceGeneral)
	euro.Price.Currency = "EUR"
	expired := offer(2, fixed(30000), domain.AudienceGeneral)
	expired.ValidUntil = ptr(at(14, 59))
	validAtStart := offer(2, fixed(30000), domain.AudienceGeneral)
	validAtStart.ValidUntil = ptr(at(15, 0))

	cases := []struct {
		name    string
		policy  func(*Policy)
		visit   domain.Candidate
		ok      bool
		offer   byte
		status  domain.PriceStatus
		program string
	}{
		{"offer accepting the card keeps its price", withPrograms, concert(offer(1, fixed(50000), domain.AudienceGeneral, domain.ProgramPushkinCard)), true, 1, domain.PriceFixed, domain.ProgramPushkinCard},
		{"no program the user does not have", none, concert(offer(1, fixed(50000), domain.AudienceGeneral, domain.ProgramPushkinCard)), true, 1, domain.PriceFixed, ""},
		{"audience tariff needs a claim", none, concert(offer(1, fixed(80000), domain.AudienceGeneral), offer(2, fixed(30000), domain.AudienceStudent)), true, 1, domain.PriceFixed, ""},
		{"claimed audience tariff", withStudent, concert(offer(1, fixed(80000), domain.AudienceGeneral), offer(2, fixed(30000), domain.AudienceStudent)), true, 2, domain.PriceFixed, ""},
		{"age-limited tariff is not assumed", none, concert(offer(1, fixed(80000), domain.AudienceGeneral), ageLimited), true, 1, domain.PriceFixed, ""},
		{"only age-limited tariffs leave the price unknown", none, concert(ageLimited), true, 0, domain.PriceUnknown, ""},
		{"tariff in another currency", none, concert(euro), true, 0, domain.PriceUnknown, ""},
		{"tariff expiring before the session", none, concert(expired), true, 0, domain.PriceUnknown, ""},
		{"tariff valid until the session starts", none, concert(validAtStart), true, 2, domain.PriceFixed, ""},
		{"cheapest by upper bound", none, concert(offer(1, between(10000, 90000), domain.AudienceGeneral), offer(2, fixed(60000), domain.AudienceGeneral)), true, 2, domain.PriceFixed, ""},
		{"known price before unknown", none, concert(offer(1, unknown(), domain.AudienceGeneral), offer(2, fixed(60000), domain.AudienceGeneral)), true, 2, domain.PriceFixed, ""},
		{"equal prices broken by offer id", none, concert(offer(3, fixed(50000), domain.AudienceGeneral), offer(2, fixed(50000), domain.AudienceGeneral)), true, 2, domain.PriceFixed, ""},
		{"only unknown offers", none, concert(offer(3, unknown(), domain.AudienceGeneral), offer(2, unknown(), domain.AudienceGeneral)), true, 2, domain.PriceUnknown, ""},
		{"place visit has no price", none, museum(), true, 0, domain.PriceUnknown, ""},
		{"strict budget refuses an unknown price", withStrict, museum(), false, 0, "", ""},
		{"strict budget with accepted unknown price", accepting, museum(), true, 0, domain.PriceUnknown, ""},
		{"strict budget takes a known price", withStrict, concert(offer(1, fixed(50000), domain.AudienceGeneral)), true, 1, domain.PriceFixed, ""},
		{"card only refuses an unknown price", pushkinOnly, museum(), false, 0, "", ""},
		{"card only with accepted unknown price", pushkinAccepting, museum(), true, 0, domain.PriceUnknown, ""},
		{"card only allows a free visit", pushkinOnly, concert(offer(1, free(), domain.AudienceGeneral)), true, 1, domain.PriceFree, ""},
		{"card only allows a covered visit", pushkinOnly, concert(offer(1, fixed(50000), domain.AudienceGeneral, domain.ProgramPushkinCard)), true, 1, domain.PriceFixed, ""},
		{"card only refuses a paid uncovered visit", pushkinAccepting, concert(offer(1, fixed(50000), domain.AudienceGeneral)), false, 0, "", ""},
		{"card only prefers a covered tariff", pushkinOnly, concert(offer(1, fixed(20000), domain.AudienceGeneral), offer(2, fixed(50000), domain.AudienceGeneral, domain.ProgramPushkinCard)), true, 2, domain.PriceFixed, ""},
	}
	for _, tc := range cases {
		p := policy()
		tc.policy(&p)
		q, ok := p.Quote(&tc.visit)
		if ok != tc.ok {
			t.Errorf("%s: ok = %v", tc.name, ok)
			continue
		}
		if !ok {
			continue
		}
		var offerID byte
		if q.Offer != nil {
			offerID = q.Offer.ID[0]
		}
		if offerID != tc.offer || q.Price.Status != tc.status || q.Program != tc.program || q.Price.Currency != "RUB" {
			t.Errorf("%s: offer %d price %+v program %q", tc.name, offerID, q.Price, q.Program)
		}
	}
}

func TestFits(t *testing.T) {
	p := policy()
	p.Budget = strict(100000)
	known := Quote{Price: fixed(40000)}
	if !p.Fits(rub(60000), known) {
		t.Fatal("price reaching the limit exactly refused")
	}
	if p.Fits(rub(60001), known) {
		t.Fatal("price over the limit accepted")
	}
	if !p.Fits(rub(100000), Quote{Price: unknown()}) {
		t.Fatal("accepted unknown price refused by the budget")
	}
	p.Budget = domain.Budget{Mode: domain.BudgetAdvisory, Limit: ptr(rub(100000))}
	if !p.Fits(rub(100000), known) {
		t.Fatal("advisory budget refused a visit")
	}
}
