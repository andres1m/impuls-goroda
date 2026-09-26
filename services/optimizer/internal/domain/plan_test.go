package domain

import (
	"math"
	"testing"
	"time"
)

func money(amount int64) Money { return Money{AmountMinor: amount, Currency: rub} }

func moneyPtr(amount int64) *Money {
	m := money(amount)
	return &m
}

func freePrice() Price {
	return Price{Status: PriceFree, Currency: rub, LowerMinor: i64(0), UpperMinor: i64(0)}
}

func visitStep() Step {
	return Step{
		VisitID:      VisitID{1},
		Kind:         StepVisit,
		Position:     1,
		ArrivalAt:    at(10, 0),
		VisitStartAt: at(10, 0),
		VisitEndAt:   at(11, 0),
		DepartureAt:  at(11, 0),
		MinDuration:  time.Hour,
		Participation: Participation{
			Status:   ParticipationActionRequired,
			Evidence: EvidenceNone,
		},
		Catalog: &CatalogSnapshot{
			PlaceID:      PlaceID{1},
			EventID:      &EventID{2},
			SessionID:    &SessionID{3},
			Title:        "Exhibition",
			Category:     CategoryCulture,
			InterestMask: Interests(InterestContemporaryArt),
			Availability: AvailabilityAvailable,
			SessionStart: timePtr(at(10, 0)),
			SessionEnd:   timePtr(at(18, 0)),
			DataMode:     DataLive,
			Provenance:   provided,
		},
		Cost: &CostSnapshot{
			PriceOfferID:   &PriceOfferID{4},
			Audience:       AudienceGeneral,
			Price:          Price{Status: PriceFixed, Currency: rub, LowerMinor: i64(50000), UpperMinor: i64(50000)},
			PersonalAmount: moneyPtr(50000),
			Provenance:     provided,
		},
		AppliedConstraints: []AppliedConstraint{{Code: "FIXED_SESSION", Strength: StrengthHard, Outcome: OutcomeSatisfied, Message: "Starts with the session"}},
	}
}

func freeTimeStep() Step {
	return Step{
		VisitID:       VisitID{2},
		Kind:          StepFreeTime,
		Position:      2,
		ArrivalAt:     at(11, 20),
		VisitStartAt:  at(11, 20),
		VisitEndAt:    at(12, 0),
		DepartureAt:   at(12, 0),
		Participation: Participation{Status: ParticipationNotRequired, Evidence: EvidenceNone},
	}
}

func leg(position int, from, to LegEndpoint, fromVisit, toVisit *VisitID, departure, arrival time.Time) Leg {
	distance := 800.0
	return Leg{
		Position:       position,
		From:           from,
		To:             to,
		FromVisitID:    fromVisit,
		ToVisitID:      toVisit,
		DepartureAt:    departure,
		ArrivalAt:      arrival,
		Mode:           "walk",
		DistanceMeters: &distance,
		Geometry:       []Coordinate{{Longitude: 37.60, Latitude: 55.70}, {Longitude: 37.61, Latitude: 55.71}},
		Verification:   VerificationEstimated,
		Evidence:       LegEvidence{Provider: "optimizer", Method: "distance_estimate", ObservedAt: fetched, Mode: "walk"},
		Cost:           CostSnapshot{Price: freePrice(), PersonalAmount: moneyPtr(0), Provenance: provided},
	}
}

func validPlan() Plan {
	first, second := VisitID{1}, VisitID{2}
	return Plan{
		Archetype:       ArchetypeUrbanAvantgarde,
		Start:           at(9, 0),
		End:             at(20, 0),
		Origin:          Coordinate{Longitude: 37.59, Latitude: 55.69},
		Destination:     &Coordinate{Longitude: 37.62, Latitude: 55.72},
		CatalogRevision: 42,
		Result:          ResultReady,
		Cost: CostSummary{
			KnownPersonal:    money(50000),
			KnownTransport:   money(0),
			ProgramAmount:    money(0),
			TotalLower:       moneyPtr(50000),
			TotalUpper:       moneyPtr(50000),
			BudgetConclusion: BudgetNotApplicable,
		},
		Steps: []Step{visitStep(), freeTimeStep()},
		Legs: []Leg{
			leg(1, EndpointOrigin, EndpointVisit, nil, &first, at(9, 40), at(10, 0)),
			leg(2, EndpointVisit, EndpointVisit, &first, &second, at(11, 0), at(11, 20)),
			leg(3, EndpointVisit, EndpointDestination, &second, nil, at(12, 0), at(12, 30)),
		},
	}
}

func TestPlanValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Plan)
		ok     bool
	}{
		{"valid", func(*Plan) {}, true},
		{"without destination", func(p *Plan) { p.Destination = nil; p.Legs = p.Legs[:2] }, true},
		{"arrival after start", func(p *Plan) { p.Steps[0].ArrivalAt = at(10, 5) }, false},
		{"shorter than minimum", func(p *Plan) { p.Steps[0].MinDuration = 2 * time.Hour }, false},
		{"visit without catalog", func(p *Plan) { p.Steps[0].Catalog = nil }, false},
		{"free time with cost", func(p *Plan) { p.Steps[1].Cost = p.Steps[0].Cost }, false},
		{"zero arrival", func(p *Plan) { p.Steps[1].ArrivalAt = time.Time{} }, false},
		{"duplicate visit", func(p *Plan) {
			p.Steps[1].VisitID = VisitID{1}
			p.Legs[1].ToVisitID, p.Legs[2].FromVisitID = &VisitID{1}, &VisitID{1}
		}, false},
		{"position gap", func(p *Plan) { p.Steps[1].Position = 3 }, false},
		{"overlapping steps", func(p *Plan) {
			p.Steps[1].ArrivalAt, p.Steps[1].VisitStartAt = at(10, 30), at(10, 30)
			p.Legs[1].DepartureAt, p.Legs[1].ArrivalAt = at(10, 10), at(10, 30)
		}, false},
		{"leg leaves before departure", func(p *Plan) { p.Legs[1].DepartureAt = at(10, 50) }, false},
		{"leg arrives after arrival", func(p *Plan) { p.Legs[1].ArrivalAt = at(11, 30) }, false},
		{"missing destination leg", func(p *Plan) { p.Legs = p.Legs[:2] }, false},
		{"origin leg with source visit", func(p *Plan) { p.Legs[0].FromVisitID = &VisitID{9} }, false},
		{"leg to another visit", func(p *Plan) { p.Legs[1].ToVisitID = &VisitID{1} }, false},
		{"distance NaN", func(p *Plan) { d := math.NaN(); p.Legs[0].DistanceMeters = &d }, false},
		{"step before day start", func(p *Plan) { p.Start = at(10, 30) }, false},
		{"arrival after day end", func(p *Plan) { p.End = at(12, 10) }, false},
		{"foreign step currency", func(p *Plan) {
			p.Steps[0].Cost.Price.Currency = "USD"
			p.Steps[0].Cost.PersonalAmount = &Money{AmountMinor: 50000, Currency: "USD"}
		}, false},
		{"ready without steps", func(p *Plan) { p.Steps, p.Legs = nil, nil }, false},
		{"no route without steps", func(p *Plan) { p.Result, p.Steps, p.Legs = ResultNoFeasibleRoute, nil, nil }, true},
		{"no route with steps", func(p *Plan) { p.Result = ResultNoFeasibleRoute }, false},
		{"conflict without conflicts", func(p *Plan) { p.Result, p.Steps, p.Legs = ResultConflict, nil, nil }, false},
		{"conflict with conflict", func(p *Plan) {
			p.Result, p.Steps, p.Legs = ResultConflict, nil, nil
			p.Conflicts = []Conflict{{Code: "SESSION_UNREACHABLE", SessionIDs: []SessionID{{3}}, Message: "Cannot reach the session"}}
		}, true},
		{"bad warning code", func(p *Plan) { p.Warnings = []Warning{{Code: "bad-code", Scope: ScopeRoute, Message: "x"}} }, false},
		{"visit warning for unknown visit", func(p *Plan) {
			p.Warnings = []Warning{{Code: "LATE", Scope: ScopeVisit, VisitID: &VisitID{9}, Message: "x"}}
		}, false},
		{"leg warning", func(p *Plan) {
			p.Warnings = []Warning{{Code: "ESTIMATED", Scope: ScopeLeg, LegPosition: intPtr(2), Message: "x"}}
		}, true},
		{"provider confirmation without provider evidence", func(p *Plan) {
			p.Steps[0].Participation = Participation{Status: ParticipationProviderConfirmed, Evidence: EvidenceUser}
		}, false},
		{"transport above personal", func(p *Plan) { p.Cost.KnownTransport = money(60000) }, false},
		{"one total bound", func(p *Plan) { p.Cost.TotalUpper = nil }, false},
		{"invalid archetype", func(p *Plan) { p.Archetype = "" }, false},
		{"session snapshot without event", func(p *Plan) { p.Steps[0].Catalog.EventID = nil }, false},
		{"session snapshot without end", func(p *Plan) { p.Steps[0].Catalog.SessionEnd = nil }, false},
		{"session snapshot ends before start", func(p *Plan) { p.Steps[0].Catalog.SessionEnd = timePtr(at(9, 0)) }, false},
		{"personal amount in other currency", func(p *Plan) { p.Steps[0].Cost.PersonalAmount = &Money{AmountMinor: 1, Currency: "USD"} }, false},
		{"visit without minimum duration", func(p *Plan) { p.Steps[0].MinDuration = 0 }, false},
		{"warning for missing leg", func(p *Plan) {
			p.Warnings = []Warning{{Code: "ESTIMATED", Scope: ScopeLeg, LegPosition: intPtr(5), Message: "x"}}
		}, false},
		{"last step after day end without destination", func(p *Plan) {
			p.Destination, p.Legs, p.End = nil, p.Legs[:2], at(11, 50)
		}, false},
		{"first leg from a visit", func(p *Plan) { p.Legs[0].From, p.Legs[0].FromVisitID = EndpointVisit, &VisitID{2} }, false},
		{"leg from another visit", func(p *Plan) { p.Legs[1].FromVisitID = &VisitID{2} }, false},
		{"last leg to a visit", func(p *Plan) { p.Legs[2].To, p.Legs[2].ToVisitID = EndpointVisit, &VisitID{1} }, false},
		{"leg position gap", func(p *Plan) { p.Legs[1].Position = 3 }, false},
		{"leg arrives before it departs", func(p *Plan) { p.Legs[0].ArrivalAt = at(9, 30) }, false},
		{"destination leg with target visit", func(p *Plan) { p.Legs[2].ToVisitID = &VisitID{2} }, false},
		{"negative distance", func(p *Plan) { d := -1.0; p.Legs[0].DistanceMeters = &d }, false},
		{"transport in other currency", func(p *Plan) { p.Cost.KnownTransport = Money{Currency: "USD"} }, false},
		{"total bound in other currency", func(p *Plan) { p.Cost.TotalUpper = &Money{AmountMinor: 50000, Currency: "USD"} }, false},
		{"total range inverted", func(p *Plan) { p.Cost.TotalLower = moneyPtr(60000) }, false},
		{"end equals start", func(p *Plan) { p.End = p.Start }, false},
		{"free time with negative minimum", func(p *Plan) { p.Steps[1].MinDuration = -time.Minute }, false},
		{"route warning with visit", func(p *Plan) {
			p.Warnings = []Warning{{Code: "LATE", Scope: ScopeRoute, VisitID: &VisitID{1}, Message: "x"}}
		}, false},
		{"visit warning with leg", func(p *Plan) {
			p.Warnings = []Warning{{Code: "LATE", Scope: ScopeVisit, VisitID: &VisitID{1}, LegPosition: intPtr(1), Message: "x"}}
		}, false},
		{"leg warning at position zero", func(p *Plan) {
			p.Warnings = []Warning{{Code: "ESTIMATED", Scope: ScopeLeg, LegPosition: intPtr(0), Message: "x"}}
		}, false},
		{"session snapshot zero start", func(p *Plan) { p.Steps[0].Catalog.SessionStart = &time.Time{} }, false},
		{"provenance zero verification time", func(p *Plan) { p.Steps[0].Catalog.Provenance.VerifiedAt = &time.Time{} }, false},
		{"provenance zero source update", func(p *Plan) { p.Steps[0].Catalog.Provenance.SourceUpdatedAt = &time.Time{} }, false},
		{"provenance blank url", func(p *Plan) { blank := " "; p.Steps[0].Catalog.Provenance.SourceURL = &blank }, false},
		{"cost with unknown audience", func(p *Plan) { p.Steps[0].Cost.Audience = "vip" }, false},
		{"leg cost without audience", func(p *Plan) { p.Legs[0].Cost.Audience = "" }, true},
		{"bad destination", func(p *Plan) { p.Destination = &Coordinate{Latitude: 100} }, false},
		{"infeasible plan with zero start", func(p *Plan) { p.Result, p.Steps, p.Legs, p.Start = ResultNoFeasibleRoute, nil, nil, time.Time{} }, false},
		{"infeasible plan ending at start", func(p *Plan) { p.Result, p.Steps, p.Legs, p.End = ResultNoFeasibleRoute, nil, nil, p.Start }, false},
		{"infeasible plan with legs", func(p *Plan) { p.Result, p.Steps = ResultNoFeasibleRoute, nil }, false},
		{"snapshot without title", func(p *Plan) { p.Steps[0].Catalog.Title = " " }, false},
		{"constraint strength unknown", func(p *Plan) { p.Steps[0].AppliedConstraints[0].Strength = "firm" }, false},
		{"constraint outcome unknown", func(p *Plan) { p.Steps[0].AppliedConstraints[0].Outcome = "maybe" }, false},
		{"free time ends at start", func(p *Plan) { p.Steps[1].VisitEndAt = p.Steps[1].VisitStartAt }, false},
		{"departure before visit end", func(p *Plan) { p.Steps[1].DepartureAt = at(11, 50) }, false},
		{"visit without cost", func(p *Plan) { p.Steps[0].Cost = nil }, false},
		{"free time with catalog", func(p *Plan) { p.Steps[1].Catalog = p.Steps[0].Catalog }, false},
		{"evidence without provider", func(p *Plan) { p.Legs[0].Evidence.Provider = "" }, false},
		{"evidence without method", func(p *Plan) { p.Legs[0].Evidence.Method = "" }, false},
		{"evidence without mode", func(p *Plan) { p.Legs[0].Evidence.Mode = "" }, false},
		{"visit leg without source visit", func(p *Plan) { p.Legs[1].FromVisitID = nil }, false},
		{"middle leg from origin", func(p *Plan) { p.Legs[1].From, p.Legs[1].FromVisitID = EndpointOrigin, nil }, false},
		{"first leg to destination", func(p *Plan) { p.Legs[0].To, p.Legs[0].ToVisitID = EndpointDestination, nil }, false},
		{"route warning with leg", func(p *Plan) {
			p.Warnings = []Warning{{Code: "LATE", Scope: ScopeRoute, LegPosition: intPtr(1), Message: "x"}}
		}, false},
		{"leg warning with visit", func(p *Plan) {
			p.Warnings = []Warning{{Code: "LATE", Scope: ScopeLeg, VisitID: &VisitID{1}, LegPosition: intPtr(1), Message: "x"}}
		}, false},
		{"leg warning without position", func(p *Plan) { p.Warnings = []Warning{{Code: "LATE", Scope: ScopeLeg, Message: "x"}} }, false},
		{"leg in other currency", func(p *Plan) {
			p.Legs[1].Cost.Price.Currency = "USD"
			p.Legs[1].Cost.PersonalAmount = &Money{Currency: "USD"}
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validPlan()
			tt.mutate(&p)
			if err := p.Validate(); (err == nil) != tt.ok {
				t.Fatalf("Validate() = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

func TestPlanValidateBudget(t *testing.T) {
	strict := Budget{Mode: BudgetStrict, Limit: moneyPtr(100000)}
	tests := []struct {
		name   string
		budget Budget
		mutate func(*CostSummary)
		ok     bool
	}{
		{"no budget", Budget{Mode: BudgetNone}, func(*CostSummary) {}, true},
		{"no budget but concluded", Budget{Mode: BudgetNone}, func(c *CostSummary) { c.BudgetConclusion = BudgetSatisfied }, false},
		{"strict satisfied", strict, func(c *CostSummary) { c.BudgetConclusion = BudgetSatisfied }, true},
		{"strict unknown hidden as satisfied", strict, func(c *CostSummary) {
			c.BudgetConclusion = BudgetSatisfied
			c.UnknownComponents = []UnknownCostComponent{{Code: "PRICE_UNKNOWN", Message: "Ticket price is unknown"}}
		}, false},
		{"strict unknown", strict, func(c *CostSummary) {
			c.BudgetConclusion = BudgetUnknown
			c.UnknownComponents = []UnknownCostComponent{{Code: "PRICE_UNKNOWN", Message: "Ticket price is unknown"}}
		}, true},
		{"strict not concluded", strict, func(c *CostSummary) {}, false},
		{"limit in other currency", Budget{Mode: BudgetStrict, Limit: &Money{AmountMinor: 1, Currency: "USD"}}, func(c *CostSummary) {
			c.BudgetConclusion = BudgetSatisfied
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validPlan()
			tt.mutate(&p.Cost)
			if err := p.ValidateBudget(tt.budget); (err == nil) != tt.ok {
				t.Fatalf("ValidateBudget() = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

func TestStepAndLegRejectZeroTimes(t *testing.T) {
	step := visitStep()
	step.ArrivalAt = time.Time{}
	if err := step.Validate(); err == nil {
		t.Fatal("step with zero arrival accepted")
	}
	first := VisitID{1}
	l := leg(1, EndpointOrigin, EndpointVisit, nil, &first, time.Time{}, at(10, 0))
	if err := l.Validate(); err == nil {
		t.Fatal("leg with zero departure accepted")
	}
	evidence := leg(1, EndpointOrigin, EndpointVisit, nil, &first, at(9, 40), at(10, 0))
	evidence.Evidence.ObservedAt = time.Time{}
	if err := evidence.Validate(); err == nil {
		t.Fatal("leg evidence with zero observation time accepted")
	}
}

func TestLegPositionMustBePositive(t *testing.T) {
	first := VisitID{1}
	l := leg(0, EndpointOrigin, EndpointVisit, nil, &first, at(9, 40), at(10, 0))
	if err := l.Validate(); err == nil {
		t.Fatal("leg at position zero accepted")
	}
}

func TestStepPositionMustBePositive(t *testing.T) {
	step := freeTimeStep()
	step.Position = 0
	if err := step.Validate(); err == nil {
		t.Fatal("step at position zero accepted")
	}
}

func TestAdvisoryBudgetMustConclude(t *testing.T) {
	p := validPlan()
	if err := p.ValidateBudget(Budget{Mode: BudgetAdvisory, Limit: moneyPtr(1)}); err == nil {
		t.Fatal("advisory budget left unconcluded on a plan with steps")
	}
}

func TestInfeasiblePlanMayLeaveBudgetUnconcluded(t *testing.T) {
	p := validPlan()
	p.Result, p.Steps, p.Legs = ResultNoFeasibleRoute, nil, nil
	if err := p.ValidateBudget(Budget{Mode: BudgetStrict, Limit: moneyPtr(1)}); err != nil {
		t.Fatalf("empty plan with not_applicable rejected: %v", err)
	}
}
