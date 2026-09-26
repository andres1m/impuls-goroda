package domain

import (
	"testing"
	"time"
)

func validConstraints() RouteConstraints {
	return RouteConstraints{
		InterestMask:  Interests(InterestContemporaryArt, InterestCinema),
		MovementModes: []MovementMode{"walk", "transit"},
		LoadProfile:   "moderate",
		Budget:        Budget{Mode: BudgetStrict, Limit: moneyPtr(300000)},
		Obligations: []Obligation{{
			SessionID:     &SessionID{3},
			StartsAt:      timePtr(at(10, 0)),
			ArrivalBuffer: 15 * time.Minute,
			Participation: ParticipationUserReported,
		}},
		LunchWindow: &LunchWindow{Start: at(13, 0), End: at(14, 30), MinDuration: 45 * time.Minute},
	}
}

func TestRouteConstraintsValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RouteConstraints)
		ok     bool
	}{
		{"valid", func(*RouteConstraints) {}, true},
		{"no movement modes", func(c *RouteConstraints) { c.MovementModes = nil }, false},
		{"blank movement mode", func(c *RouteConstraints) { c.MovementModes = []MovementMode{" "} }, false},
		{"blank soft preference", func(c *RouteConstraints) { c.SoftPreferences = []string{""} }, false},
		{"blank accepted unknown", func(c *RouteConstraints) { c.AcceptedUnknowns = []string{" "} }, false},
		{"unknown excluded category", func(c *RouteConstraints) { c.ExcludedCategories = []Category{"nightlife"} }, false},
		{"no load profile", func(c *RouteConstraints) { c.LoadProfile = "" }, false},
		{"none budget with limit", func(c *RouteConstraints) { c.Budget = Budget{Mode: BudgetNone, Limit: moneyPtr(1)} }, false},
		{"strict budget without limit", func(c *RouteConstraints) { c.Budget = Budget{Mode: BudgetStrict} }, false},
		{"obligation without ids", func(c *RouteConstraints) { c.Obligations[0].SessionID = nil }, false},
		{"obligation zero start", func(c *RouteConstraints) { c.Obligations[0].StartsAt = &time.Time{} }, false},
		{"negative arrival buffer", func(c *RouteConstraints) { c.Obligations[0].ArrivalBuffer = -time.Minute }, false},
		{"lunch zero start", func(c *RouteConstraints) { c.LunchWindow.Start = time.Time{} }, false},
		{"lunch ends at start", func(c *RouteConstraints) { c.LunchWindow.End = c.LunchWindow.Start }, false},
		{"lunch without duration", func(c *RouteConstraints) { c.LunchWindow.MinDuration = 0 }, false},
		{"lunch longer than window", func(c *RouteConstraints) { c.LunchWindow.MinDuration = 2 * time.Hour }, false},
		{"blank program balance", func(c *RouteConstraints) { c.ProgramBalance = &ProgramBalance{Balance: money(100)} }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validConstraints()
			tt.mutate(&c)
			if err := c.Validate(); (err == nil) != tt.ok {
				t.Fatalf("Validate() = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

func validOptimizeRequest() OptimizeRequest {
	return OptimizeRequest{
		City:        "moscow",
		Timezone:    "Europe/Moscow",
		Start:       at(9, 0),
		End:         at(20, 0),
		Origin:      Coordinate{Longitude: 37.59, Latitude: 55.69},
		Constraints: validConstraints(),
	}
}

func TestOptimizeRequestValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*OptimizeRequest)
		ok     bool
	}{
		{"valid", func(*OptimizeRequest) {}, true},
		{"no city", func(r *OptimizeRequest) { r.City = "" }, false},
		{"unknown timezone", func(r *OptimizeRequest) { r.Timezone = "Mars/Base" }, false},
		{"empty timezone", func(r *OptimizeRequest) { r.Timezone = "" }, false},
		{"end before start", func(r *OptimizeRequest) { r.End = r.Start }, false},
		{"zero start", func(r *OptimizeRequest) { r.Start = time.Time{} }, false},
		{"bad destination", func(r *OptimizeRequest) { r.Destination = &Coordinate{Longitude: 200} }, false},
		{"bad constraints", func(r *OptimizeRequest) { r.Constraints.MovementModes = nil }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := validOptimizeRequest()
			tt.mutate(&r)
			if err := r.Validate(); (err == nil) != tt.ok {
				t.Fatalf("Validate() = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

func validRecomputeRequest() RecomputeRequest {
	base := validPlan()
	base.Cost.BudgetConclusion = BudgetSatisfied
	return RecomputeRequest{
		City:        "moscow",
		Timezone:    "Europe/Moscow",
		Base:        base,
		Constraints: validConstraints(),
		History: []VisitExecution{{
			VisitID:     VisitID{1},
			Status:      ExecutionCompleted,
			ActualStart: timePtr(at(10, 5)),
			ActualEnd:   timePtr(at(11, 0)),
		}},
		Trigger: PinTrigger{VisitID: VisitID{2}, Kind: PinObligation},
	}
}

func TestRecomputeRequestValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RecomputeRequest)
		ok     bool
	}{
		{"pin", func(*RecomputeRequest) {}, true},
		{"delay", func(r *RecomputeRequest) {
			r.Trigger = DelayTrigger{Mode: DelayAlreadyDelayed, EffectiveStart: at(11, 10), Position: r.Base.Origin, PositionSource: PositionDevice}
		}, true},
		{"delay without effective start", func(r *RecomputeRequest) {
			r.Trigger = DelayTrigger{Mode: DelayFutureWait, Position: r.Base.Origin, PositionSource: PositionManual}
		}, false},
		{"invalid delay mode", func(r *RecomputeRequest) {
			r.Trigger = DelayTrigger{Mode: "soon", EffectiveStart: at(11, 10), Position: r.Base.Origin, PositionSource: PositionDevice}
		}, false},
		{"invalid position source", func(r *RecomputeRequest) {
			r.Trigger = DelayTrigger{Mode: DelayFutureWait, EffectiveStart: at(11, 10), Position: r.Base.Origin, PositionSource: "gps"}
		}, false},
		{"invalid removal mode", func(r *RecomputeRequest) { r.Trigger = RemovalTrigger{VisitID: VisitID{2}, Mode: "hide"} }, false},
		{"cancellation", func(r *RecomputeRequest) {
			r.Trigger = CancellationTrigger{VisitIDs: []VisitID{{1}}, MinCatalogRevision: 43}
		}, true},
		{"cancellation without visits", func(r *RecomputeRequest) { r.Trigger = CancellationTrigger{MinCatalogRevision: 43} }, false},
		{"removal", func(r *RecomputeRequest) { r.Trigger = RemovalTrigger{VisitID: VisitID{2}, Mode: RemovalFreeTime} }, true},
		{"removal of foreign visit", func(r *RecomputeRequest) { r.Trigger = RemovalTrigger{VisitID: VisitID{9}, Mode: RemovalRebuild} }, false},
		{"pin of foreign visit", func(r *RecomputeRequest) { r.Trigger = PinTrigger{VisitID: VisitID{9}, Kind: PinPreferred} }, false},
		{"invalid pin kind", func(r *RecomputeRequest) { r.Trigger = PinTrigger{VisitID: VisitID{2}, Kind: "always"} }, false},
		{"no trigger", func(r *RecomputeRequest) { r.Trigger = nil }, false},
		{"completed without end", func(r *RecomputeRequest) { r.History[0].ActualEnd = nil }, false},
		{"completed without start", func(r *RecomputeRequest) { r.History[0].ActualStart = nil }, false},
		{"skipped with zero start", func(r *RecomputeRequest) {
			r.History[0] = VisitExecution{VisitID: VisitID{1}, Status: ExecutionSkipped, ActualStart: &time.Time{}}
		}, false},
		{"skipped with zero end", func(r *RecomputeRequest) {
			r.History[0] = VisitExecution{VisitID: VisitID{1}, Status: ExecutionSkipped, ActualEnd: &time.Time{}}
		}, false},
		{"completed ends before it starts", func(r *RecomputeRequest) { r.History[0].ActualEnd = timePtr(at(10, 0)) }, false},
		{"history of foreign visit", func(r *RecomputeRequest) { r.History[0].VisitID = VisitID{9} }, false},
		{"skipped without times", func(r *RecomputeRequest) {
			r.History[0] = VisitExecution{VisitID: VisitID{1}, Status: ExecutionSkipped}
		}, true},
		{"invalid base plan", func(r *RecomputeRequest) { r.Base.Steps[0].Position = 5 }, false},
		{"base plan hides unknown strict budget", func(r *RecomputeRequest) {
			r.Base.Cost.UnknownComponents = []UnknownCostComponent{{Code: "PRICE_UNKNOWN", Message: "Unknown"}}
		}, false},
		{"unknown timezone", func(r *RecomputeRequest) { r.Timezone = "Nowhere" }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := validRecomputeRequest()
			tt.mutate(&r)
			if err := r.Validate(); (err == nil) != tt.ok {
				t.Fatalf("Validate() = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}
