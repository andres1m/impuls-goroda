package validation

import (
	"slices"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func codes(violations []Violation) []string {
	out := make([]string, len(violations))
	for i, v := range violations {
		out[i] = v.Code
	}
	return out
}

func TestValidPlanPasses(t *testing.T) {
	plan, in := validPlan()
	if err := plan.Validate(); err != nil {
		t.Fatalf("fixture is malformed: %v", err)
	}
	if v := Check(plan, in); len(v) != 0 {
		t.Fatalf("violations %v", v)
	}
}

func TestAllowedVariationsPass(t *testing.T) {
	cases := map[string]func(*domain.Plan, *Input){
		"late entry the source allows": func(p *domain.Plan, in *Input) {
			c := in.Candidates[concertVisit]
			c.Window.LateEntryAllowed = ptr(true)
			in.Candidates[concertVisit] = c
			p.Steps[1].VisitStartAt = at(13, 10)
			p.Steps[1].MinDuration = 80 * time.Minute
			c.Window.MinDuration = 80 * time.Minute
			in.Candidates[concertVisit] = c
		},
		"accepted unknown price under a strict budget": func(p *domain.Plan, in *Input) {
			in.Constraints.Budget = domain.Budget{Mode: domain.BudgetStrict, Limit: ptr(rub(60000))}
			in.Constraints.AcceptedUnknowns = []string{domain.AcceptUnknownPrice}
			p.Cost.BudgetConclusion = domain.BudgetUnknown
			p.Result = domain.ResultPartial
		},
		"honoured obligation": func(p *domain.Plan, in *Input) {
			in.Constraints.Obligations = []domain.Obligation{{SessionID: &domain.SessionID{2}, Participation: domain.ParticipationUserReported}}
			p.Steps[1].Obligation = true
			p.Steps[1].Participation = domain.Participation{Status: domain.ParticipationUserReported, Evidence: domain.EvidenceUser}
		},
		"sold out session the user holds a ticket for": func(p *domain.Plan, in *Input) {
			setAvailability(in, domain.AvailabilitySoldOut)
			in.Constraints.Obligations = []domain.Obligation{{SessionID: &domain.SessionID{2}, Participation: domain.ParticipationProviderConfirmed}}
			p.Steps[1].Obligation = true
			p.Steps[1].Participation = domain.Participation{Status: domain.ParticipationProviderConfirmed, Evidence: domain.EvidenceProvider}
		},
		"repeated obligation for the same session": func(p *domain.Plan, in *Input) {
			in.Constraints.Obligations = []domain.Obligation{
				{SessionID: &domain.SessionID{2}, Participation: domain.ParticipationUserReported},
				{SessionID: &domain.SessionID{2}, Participation: domain.ParticipationActionRequired},
			}
			p.Steps[1].Obligation = true
			p.Steps[1].Participation = domain.Participation{Status: domain.ParticipationUserReported, Evidence: domain.EvidenceUser}
		},
		"late entrant arriving inside the buffer": func(p *domain.Plan, in *Input) {
			c := in.Candidates[concertVisit]
			c.Window.LateEntryAllowed = ptr(true)
			in.Candidates[concertVisit] = c
			p.Legs[1].DepartureAt, p.Legs[1].ArrivalAt, p.Steps[1].ArrivalAt = at(12, 40), at(12, 55), at(12, 55)
			p.Steps[0].VisitEndAt, p.Steps[0].DepartureAt = at(12, 40), at(12, 40)
		},
		"claimed audience tariff": func(p *domain.Plan, in *Input) {
			c := in.Candidates[concertVisit]
			c.Offers = []domain.PriceOffer{c.Offers[0]}
			c.Offers[0].Audience = domain.AudienceStudent
			in.Candidates[concertVisit] = c
			in.Constraints.AudienceClaims = []string{"student"}
			p.Steps[1].Cost.Audience = domain.AudienceStudent
		},
		"sold out session with a ticket the user reports": func(p *domain.Plan, in *Input) {
			setAvailability(in, domain.AvailabilitySoldOut)
			in.Constraints.Obligations = []domain.Obligation{{SessionID: &domain.SessionID{2}, Participation: domain.ParticipationUserReported}}
			p.Steps[1].Obligation = true
			p.Steps[1].Participation = domain.Participation{Status: domain.ParticipationUserReported, Evidence: domain.EvidenceUser}
		},
		"free visit in card only mode": func(p *domain.Plan, in *Input) {
			c := in.Candidates[concertVisit]
			c.Offers = []domain.PriceOffer{c.Offers[0]}
			c.Offers[0].Price, c.Offers[0].BenefitPrograms = free(), nil
			in.Candidates[concertVisit] = c
			in.Constraints.PushkinCardOnly = true
			in.Constraints.AcceptedUnknowns = []string{domain.AcceptUnknownPrice}
			p.Steps[1].Cost.Price, p.Steps[1].Cost.PersonalAmount = free(), ptr(rub(0))
			p.Cost.KnownPersonal = rub(0)
			p.Result = domain.ResultPartial
		},
		"program payment for a tariff that takes the card": func(p *domain.Plan, in *Input) {
			in.Constraints.BenefitPrograms = []string{domain.ProgramPushkinCard}
			p.Steps[1].Cost.PersonalAmount, p.Steps[1].Cost.ProgramAmount = nil, ptr(rub(50000))
			p.Cost.KnownPersonal, p.Cost.ProgramAmount = rub(0), rub(50000)
		},
		"pinned session in a category the user excluded": func(p *domain.Plan, in *Input) {
			in.Constraints.ExcludedCategories = []domain.Category{domain.CategoryCulture}
			in.Constraints.Obligations = []domain.Obligation{{SessionID: &domain.SessionID{2}, Participation: domain.ParticipationActionRequired}}
			p.Steps[1].Obligation = true
			delete(in.Candidates, museumVisit)
			p.Steps, p.Legs = p.Steps[1:], []domain.Leg{walk(1, nil, &concertVisit, at(10, 0), at(10, 15)), walk(2, &concertVisit, nil, at(14, 30), at(14, 40))}
			p.Steps[0].Position, p.Steps[0].ArrivalAt = 1, at(10, 15)
			p.Cost.UnknownComponents = nil
			p.Cost.TotalLower, p.Cost.TotalUpper = ptr(rub(50000)), ptr(rub(50000))
		},
		"history the window would not allow": func(p *domain.Plan, in *Input) {
			c := in.Candidates[museumVisit]
			c.Window.Start = at(10, 30)
			in.Candidates[museumVisit] = c
			in.History = map[domain.VisitID]struct{}{museumVisit: {}}
		},
		"history whose place left the catalog": func(p *domain.Plan, in *Input) {
			delete(in.Candidates, concertVisit)
			in.History = map[domain.VisitID]struct{}{concertVisit: {}}
		},
		"history bought at a tariff gone since": func(p *domain.Plan, in *Input) {
			c := in.Candidates[concertVisit]
			c.Offers = nil
			in.Candidates[concertVisit] = c
			in.History = map[domain.VisitID]struct{}{concertVisit: {}}
		},
		"history of a session cancelled since": func(p *domain.Plan, in *Input) {
			setAvailability(in, domain.AvailabilityCancelled)
			in.History = map[domain.VisitID]struct{}{concertVisit: {}}
		},
		"history reached over a leg routing now cannot confirm": func(p *domain.Plan, in *Input) {
			in.Degraded = true
			in.History = map[domain.VisitID]struct{}{museumVisit: {}}
			p.Legs[1].Verification, p.Legs[2].Verification = domain.VerificationUnknown, domain.VerificationUnknown
		},
		"degraded routing marks legs unknown": func(p *domain.Plan, in *Input) {
			in.Degraded = true
			for i := range p.Legs {
				p.Legs[i].Verification = domain.VerificationUnknown
			}
		},
	}
	for name, change := range cases {
		plan, in := validPlan()
		change(&plan, &in)
		if v := Check(plan, in); len(v) != 0 {
			t.Errorf("%s: violations %v", name, v)
		}
	}
}

func TestEveryRuleCatchesItsViolation(t *testing.T) {
	cases := []struct {
		name   string
		code   string
		change func(*domain.Plan, *Input)
	}{
		{"missing visit obligation", "OBLIGATION_MISSING", func(_ *domain.Plan, in *Input) {
			in.Constraints.Obligations = []domain.Obligation{{VisitID: &domain.VisitID{8}, Participation: domain.ParticipationActionRequired}}
		}},
		{"malformed plan", "PLAN_MALFORMED", func(p *domain.Plan, _ *Input) { p.Steps[0].Position = 5 }},
		{"step without a candidate", "STEP_WITHOUT_CANDIDATE", func(_ *domain.Plan, in *Input) { delete(in.Candidates, museumVisit) }},
		{"snapshot of another place", "STEP_CATALOG_MISMATCH", func(p *domain.Plan, _ *Input) { p.Steps[0].Catalog.PlaceID = domain.PlaceID{9} }},
		{"planned return to a place already visited", "PLACE_REPEATED", func(p *domain.Plan, in *Input) {
			c := in.Candidates[concertVisit]
			c.Place.ID = domain.PlaceID{1}
			in.Candidates[concertVisit] = c
			p.Steps[1].Catalog.PlaceID = domain.PlaceID{1}
			delete(in.Candidates, museumVisit)
			in.History = map[domain.VisitID]struct{}{museumVisit: {}}
		}},
		{"same place twice", "PLACE_REPEATED", func(p *domain.Plan, in *Input) {
			c := in.Candidates[concertVisit]
			c.Place.ID = domain.PlaceID{1}
			in.Candidates[concertVisit] = c
			p.Steps[1].Catalog.PlaceID = domain.PlaceID{1}
		}},
		{"excluded category", "CATEGORY_EXCLUDED", func(_ *domain.Plan, in *Input) {
			in.Constraints.ExcludedCategories = []domain.Category{domain.CategoryCulture}
		}},
		{"cancelled session", "SESSION_CANCELLED", func(_ *domain.Plan, in *Input) { setAvailability(in, domain.AvailabilityCancelled) }},
		{"sold out without a ticket", "SESSION_SOLD_OUT", func(_ *domain.Plan, in *Input) { setAvailability(in, domain.AvailabilitySoldOut) }},
		{"fixed session ending after its window", "FIXED_SESSION_MOVED", func(_ *domain.Plan, in *Input) {
			c := in.Candidates[concertVisit]
			c.Window.End = at(14, 20)
			in.Candidates[concertVisit] = c
		}},
		{"late entry after the default last entry", "LAST_ENTRY_MISSED", func(p *domain.Plan, in *Input) {
			c := in.Candidates[concertVisit]
			c.Window.LateEntryAllowed, c.Window.MinDuration = ptr(true), 60*time.Minute
			in.Candidates[concertVisit] = c
			p.Steps[1].VisitStartAt, p.Steps[1].MinDuration = at(13, 45), 45*time.Minute
		}},
		{"buffer the user sees broken", "ARRIVAL_BUFFER_MISSED", func(p *domain.Plan, _ *Input) { p.Steps[1].ArrivalAt = at(12, 55) }},
		{"snapshot of another session", "STEP_CATALOG_MISMATCH", func(p *domain.Plan, _ *Input) { p.Steps[1].Catalog.SessionID = &domain.SessionID{9} }},
		{"obligation mark without an obligation", "OBLIGATION_UNEXPECTED", func(p *domain.Plan, _ *Input) { p.Steps[1].Obligation = true }},
		{"expired tariff", "PRICE_OFFER_NOT_APPLICABLE", func(_ *domain.Plan, in *Input) {
			changeOffer(in, func(o *domain.PriceOffer) { o.ValidUntil = ptr(at(12, 0)) })
		}},
		{"age limited tariff", "PRICE_OFFER_NOT_APPLICABLE", func(_ *domain.Plan, in *Input) {
			changeOffer(in, func(o *domain.PriceOffer) { o.EligibilityAgeMax = ptr(22) })
		}},
		{"tariff in another currency", "PRICE_OFFER_NOT_APPLICABLE", func(_ *domain.Plan, in *Input) { in.Currency = "EUR" }},
		{"unknown price in card only mode without consent", "UNKNOWN_PRICE_NOT_ACCEPTED", func(p *domain.Plan, in *Input) {
			in.Constraints.PushkinCardOnly = true
			p.Result = domain.ResultPartial
		}},
		{"ready although card only mode relies on an accepted unknown", "STATUS_MISMATCH", func(_ *domain.Plan, in *Input) {
			in.Constraints.PushkinCardOnly = true
			in.Constraints.AcceptedUnknowns = []string{domain.AcceptUnknownPrice}
		}},
		{"program payment the tariff does not take", "PROGRAM_SHARE_INVENTED", func(p *domain.Plan, _ *Input) {
			p.Steps[1].Cost.PersonalAmount, p.Steps[1].Cost.ProgramAmount = nil, ptr(rub(50000))
			p.Cost.KnownPersonal, p.Cost.ProgramAmount = rub(0), rub(50000)
		}},
		{"program payment below the price", "PROGRAM_SHARE_INVENTED", func(p *domain.Plan, in *Input) {
			in.Constraints.BenefitPrograms = []string{domain.ProgramPushkinCard}
			p.Steps[1].Cost.PersonalAmount, p.Steps[1].Cost.ProgramAmount = nil, ptr(rub(20000))
			p.Cost.KnownPersonal, p.Cost.ProgramAmount = rub(0), rub(20000)
		}},
		{"understated personal share", "PERSONAL_SHARE_MISMATCH", func(p *domain.Plan, _ *Input) {
			p.Steps[1].Cost.PersonalAmount = ptr(rub(10000))
			p.Cost.KnownPersonal = rub(10000)
		}},
		{"summary program total off", "COST_SUMMARY_MISMATCH", func(p *domain.Plan, _ *Input) { p.Cost.ProgramAmount = rub(5) }},
		{"summary prices travel", "COST_SUMMARY_MISMATCH", func(p *domain.Plan, _ *Input) {
			p.Cost.KnownTransport = rub(100)
		}},
		{"summary in another currency", "COST_SUMMARY_MISMATCH", func(_ *domain.Plan, in *Input) { in.Currency = "EUR" }},
		{"invalid budget in the input", "INPUT_INVALID", func(_ *domain.Plan, in *Input) {
			in.Constraints.Budget = domain.Budget{Mode: domain.BudgetStrict}
		}},
		{"fixed session moved", "FIXED_SESSION_MOVED", func(p *domain.Plan, _ *Input) {
			p.Steps[1].VisitStartAt, p.Steps[1].MinDuration = at(13, 5), 80*time.Minute
		}},
		{"late entry past the last entry", "LAST_ENTRY_MISSED", func(p *domain.Plan, in *Input) {
			c := in.Candidates[concertVisit]
			c.Window.LateEntryAllowed, c.Window.LastEntryAt = ptr(true), ptr(at(13, 5))
			in.Candidates[concertVisit] = c
			p.Steps[1].VisitStartAt = at(13, 10)
			p.Steps[1].MinDuration = 80 * time.Minute
		}},
		{"arrival buffer of the session", "ARRIVAL_BUFFER_MISSED", func(p *domain.Plan, _ *Input) {
			p.Legs[1].DepartureAt, p.Legs[1].ArrivalAt, p.Steps[1].ArrivalAt = at(12, 40), at(12, 55), at(12, 55)
			p.Steps[0].VisitEndAt, p.Steps[0].DepartureAt = at(12, 40), at(12, 40)
		}},
		{"arrival buffer of the obligation", "ARRIVAL_BUFFER_MISSED", func(p *domain.Plan, in *Input) {
			in.Constraints.Obligations = []domain.Obligation{{SessionID: &domain.SessionID{2}, ArrivalBuffer: 20 * time.Minute, Participation: domain.ParticipationActionRequired}}
			p.Steps[1].Obligation = true
			p.Legs[1].DepartureAt, p.Legs[1].ArrivalAt, p.Steps[1].ArrivalAt = at(12, 30), at(12, 45), at(12, 45)
			p.Steps[0].VisitEndAt, p.Steps[0].DepartureAt = at(12, 30), at(12, 30)
		}},
		{"visit before the window opens", "VISIT_OUTSIDE_WINDOW", func(_ *domain.Plan, in *Input) {
			c := in.Candidates[museumVisit]
			c.Window.Start = at(10, 30)
			in.Candidates[museumVisit] = c
		}},
		{"visit after the window closes", "VISIT_OUTSIDE_WINDOW", func(_ *domain.Plan, in *Input) {
			c := in.Candidates[museumVisit]
			c.Window.End = at(11, 0)
			in.Candidates[museumVisit] = c
		}},
		{"entry after the last entry", "LAST_ENTRY_MISSED", func(_ *domain.Plan, in *Input) {
			c := in.Candidates[museumVisit]
			c.Window.LastEntryAt = ptr(at(10, 5))
			in.Candidates[museumVisit] = c
		}},
		{"arrival buffer of an open window", "ARRIVAL_BUFFER_MISSED", func(_ *domain.Plan, in *Input) {
			c := in.Candidates[museumVisit]
			c.Window.ArrivalBuffer = 15 * time.Minute
			in.Candidates[museumVisit] = c
		}},
		{"visit shorter than its minimum", "VISIT_TOO_SHORT", func(_ *domain.Plan, in *Input) {
			c := in.Candidates[museumVisit]
			c.Window.MinDuration = 90 * time.Minute
			in.Candidates[museumVisit] = c
		}},
		{"mode the user did not allow", "LEG_MODE_NOT_ALLOWED", func(_ *domain.Plan, in *Input) {
			in.Constraints.MovementModes = []domain.MovementMode{domain.MovementTransit}
		}},
		{"leg claimed as verified", "LEG_STATUS_OVERSTATED", func(p *domain.Plan, _ *Input) { p.Legs[0].Verification = domain.VerificationVerified }},
		{"estimated leg while degraded", "LEG_STATUS_OVERSTATED", func(_ *domain.Plan, in *Input) { in.Degraded = true }},
		{"unavailable leg", "LEG_UNAVAILABLE", func(p *domain.Plan, _ *Input) { p.Legs[0].Verification = domain.VerificationUnavailable }},
		{"car leg while degraded", "LEG_MODE_UNAVAILABLE", func(p *domain.Plan, in *Input) {
			in.Degraded = true
			in.Constraints.MovementModes = append(in.Constraints.MovementModes, domain.MovementCar)
			for i := range p.Legs {
				p.Legs[i].Verification = domain.VerificationUnknown
			}
			p.Legs[0].Mode = domain.MovementCar
		}},
		{"free time marked as an obligation", "OBLIGATION_UNEXPECTED", func(p *domain.Plan, _ *Input) {
			p.Steps[0] = domain.Step{
				VisitID: museumVisit, Kind: domain.StepFreeTime, Position: 1,
				ArrivalAt: at(10, 10), VisitStartAt: at(10, 10), VisitEndAt: at(11, 10), DepartureAt: at(11, 10),
				Obligation:    true,
				Participation: domain.Participation{Status: domain.ParticipationNotRequired, Evidence: domain.EvidenceNone},
			}
		}},
		{"budget limit in another currency", "INPUT_INVALID", func(_ *domain.Plan, in *Input) {
			in.Constraints.Budget = domain.Budget{Mode: domain.BudgetAdvisory, Limit: &domain.Money{AmountMinor: 50000, Currency: "EUR"}}
		}},
		{"obligation left out", "OBLIGATION_MISSING", func(_ *domain.Plan, in *Input) {
			in.Constraints.Obligations = []domain.Obligation{{SessionID: &domain.SessionID{77}, Participation: domain.ParticipationActionRequired}}
		}},
		{"obligation not marked", "OBLIGATION_NOT_MARKED", func(_ *domain.Plan, in *Input) {
			in.Constraints.Obligations = []domain.Obligation{{SessionID: &domain.SessionID{2}, Participation: domain.ParticipationActionRequired}}
		}},
		{"obligation participation changed", "OBLIGATION_PARTICIPATION_CHANGED", func(p *domain.Plan, in *Input) {
			in.Constraints.Obligations = []domain.Obligation{{SessionID: &domain.SessionID{2}, Participation: domain.ParticipationProviderConfirmed}}
			p.Steps[1].Obligation = true
		}},
		{"offer the candidate does not have", "PRICE_OFFER_UNKNOWN", func(p *domain.Plan, _ *Input) { p.Steps[1].Cost.PriceOfferID = &domain.PriceOfferID{9} }},
		{"tariff the user may not use", "PRICE_OFFER_NOT_APPLICABLE", func(_ *domain.Plan, in *Input) {
			c := in.Candidates[concertVisit]
			c.Offers = []domain.PriceOffer{c.Offers[0]}
			c.Offers[0].Audience = domain.AudienceStudent
			in.Candidates[concertVisit] = c
		}},
		{"price differs from the offer", "PRICE_MISMATCH", func(p *domain.Plan, _ *Input) { p.Steps[1].Cost.Price = fixed(1000) }},
		{"price without an offer", "PRICE_INVENTED", func(p *domain.Plan, _ *Input) {
			p.Steps[0].Cost.Price = fixed(0)
			p.Steps[0].Cost.UnknownComponents = nil
		}},
		{"unknown price without consent", "UNKNOWN_PRICE_NOT_ACCEPTED", func(p *domain.Plan, in *Input) {
			in.Constraints.Budget = domain.Budget{Mode: domain.BudgetStrict, Limit: ptr(rub(60000))}
			p.Cost.BudgetConclusion = domain.BudgetUnknown
			p.Result = domain.ResultPartial
		}},
		{"known prices over a strict budget", "BUDGET_EXCEEDED", func(p *domain.Plan, in *Input) {
			in.Constraints.Budget = domain.Budget{Mode: domain.BudgetStrict, Limit: ptr(rub(40000))}
			in.Constraints.AcceptedUnknowns = []string{domain.AcceptUnknownPrice}
			p.Cost.BudgetConclusion = domain.BudgetUnknown
			p.Result = domain.ResultPartial
		}},
		{"paid visit the card cannot pay", "PUSHKIN_CARD_NOT_ACCEPTED", func(p *domain.Plan, in *Input) {
			c := in.Candidates[concertVisit]
			c.Offers = []domain.PriceOffer{c.Offers[0]}
			c.Offers[0].BenefitPrograms = nil
			in.Candidates[concertVisit] = c
			in.Constraints.PushkinCardOnly = true
			in.Constraints.AcceptedUnknowns = []string{domain.AcceptUnknownPrice}
			p.Result = domain.ResultPartial
		}},
		{"promised personal share next to a program payment", "PROGRAM_SHARE_PROMISED", func(p *domain.Plan, _ *Input) {
			p.Steps[1].Cost.ProgramAmount = ptr(rub(50000))
		}},
		{"summary disagrees with the steps", "COST_SUMMARY_MISMATCH", func(p *domain.Plan, _ *Input) { p.Cost.KnownPersonal = rub(1) }},
		{"wrong budget conclusion", "COST_SUMMARY_MISMATCH", func(p *domain.Plan, in *Input) {
			in.Constraints.Budget = domain.Budget{Mode: domain.BudgetAdvisory, Limit: ptr(rub(60000))}
			p.Cost.BudgetConclusion = domain.BudgetSatisfied
		}},
		{"partial without an accepted unknown", "STATUS_MISMATCH", func(p *domain.Plan, _ *Input) { p.Result = domain.ResultPartial }},
		{"ready although it relies on an accepted unknown", "STATUS_MISMATCH", func(p *domain.Plan, in *Input) {
			in.Constraints.Budget = domain.Budget{Mode: domain.BudgetStrict, Limit: ptr(rub(60000))}
			in.Constraints.AcceptedUnknowns = []string{domain.AcceptUnknownPrice}
			p.Cost.BudgetConclusion = domain.BudgetUnknown
		}},
	}
	for _, tc := range cases {
		plan, in := validPlan()
		tc.change(&plan, &in)
		got := codes(Check(plan, in))
		if !slices.Contains(got, tc.code) {
			t.Errorf("%s: got %v, want %s", tc.name, got, tc.code)
		}
	}
}

func setAvailability(in *Input, a domain.Availability) {
	c := in.Candidates[concertVisit]
	session := *c.Session
	session.Availability = a
	c.Session = &session
	in.Candidates[concertVisit] = c
}

func TestViolationsNameTheirVisit(t *testing.T) {
	plan, in := validPlan()
	c := in.Candidates[museumVisit]
	c.Window.MinDuration = 90 * time.Minute
	in.Candidates[museumVisit] = c
	v := Check(plan, in)
	if len(v) != 1 || v[0].VisitID == nil || *v[0].VisitID != museumVisit || v[0].Message == "" {
		t.Fatalf("violations %+v", v)
	}
}

// concertOnly is a route whose every price is known, so the budget reaches a firm conclusion.
func concertOnly(budget domain.Budget, conclusion domain.BudgetConclusion) (domain.Plan, Input) {
	plan, in := validPlan()
	concertStep := plan.Steps[1]
	concertStep.Position, concertStep.ArrivalAt = 1, at(10, 15)
	plan.Steps = []domain.Step{concertStep}
	plan.Legs = []domain.Leg{walk(1, nil, &concertVisit, at(10, 0), at(10, 15)), walk(2, &concertVisit, nil, at(14, 30), at(14, 40))}
	plan.Cost.UnknownComponents = nil
	plan.Cost.TotalLower, plan.Cost.TotalUpper = ptr(rub(50000)), ptr(rub(50000))
	plan.Cost.BudgetConclusion = conclusion
	delete(in.Candidates, museumVisit)
	in.Constraints.Budget = budget
	return plan, in
}

func TestBudgetConclusion(t *testing.T) {
	advisory := func(limit int64) domain.Budget {
		return domain.Budget{Mode: domain.BudgetAdvisory, Limit: ptr(rub(limit))}
	}
	cases := []struct {
		name       string
		budget     domain.Budget
		conclusion domain.BudgetConclusion
		want       []string
	}{
		{"within the budget", advisory(60000), domain.BudgetSatisfied, nil},
		{"over an advisory budget", advisory(40000), domain.BudgetViolated, nil},
		{"over but reported within", advisory(40000), domain.BudgetSatisfied, []string{"COST_SUMMARY_MISMATCH"}},
		{"over a strict budget", domain.Budget{Mode: domain.BudgetStrict, Limit: ptr(rub(40000))}, domain.BudgetViolated, []string{"BUDGET_EXCEEDED"}},
		{"totals that do not add up", advisory(60000), domain.BudgetSatisfied, []string{"COST_SUMMARY_MISMATCH"}},
	}
	for i, tc := range cases {
		plan, in := concertOnly(tc.budget, tc.conclusion)
		if i == len(cases)-1 {
			plan.Cost.TotalUpper = ptr(rub(60000))
		}
		if got := codes(Check(plan, in)); !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func changeOffer(in *Input, change func(*domain.PriceOffer)) {
	c := in.Candidates[concertVisit]
	c.Offers = []domain.PriceOffer{c.Offers[0]}
	change(&c.Offers[0])
	in.Candidates[concertVisit] = c
}

func TestBudgetLowerTotal(t *testing.T) {
	plan, in := concertOnly(domain.Budget{Mode: domain.BudgetNone}, domain.BudgetNotApplicable)
	plan.Cost.TotalLower = ptr(rub(1))
	if got := codes(Check(plan, in)); !slices.Equal(got, []string{"COST_SUMMARY_MISMATCH"}) {
		t.Fatalf("got %v", got)
	}
}
