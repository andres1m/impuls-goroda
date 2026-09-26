package domain

import (
	"testing"
	"time"
)

func TestRouteChangeValidate(t *testing.T) {
	before, after := &VisitID{1}, &VisitID{2}
	tests := []struct {
		name   string
		change RouteChange
		ok     bool
	}{
		{"kept", RouteChange{Kind: ChangeKept, Scope: ScopeVisit, BeforeVisitID: before, AfterVisitID: before, Message: "Kept"}, true},
		{"kept without previous visit", RouteChange{Kind: ChangeKept, Scope: ScopeVisit, AfterVisitID: after, Message: "x"}, false},
		{"removed", RouteChange{Kind: ChangeRemoved, Scope: ScopeVisit, BeforeVisitID: before, Message: "Removed"}, true},
		{"removed with after", RouteChange{Kind: ChangeRemoved, Scope: ScopeVisit, BeforeVisitID: before, AfterVisitID: after, Message: "x"}, false},
		{"replaced", RouteChange{Kind: ChangeReplaced, Scope: ScopeVisit, BeforeVisitID: before, AfterVisitID: after, Message: "Replaced"}, true},
		{"replaced without before", RouteChange{Kind: ChangeReplaced, Scope: ScopeVisit, AfterVisitID: after, Message: "x"}, false},
		{"time shifted", RouteChange{Kind: ChangeTimeShifted, Scope: ScopeVisit, BeforeVisitID: before, AfterVisitID: before, Message: "Later", Details: TimeShift{Delta: 20 * time.Minute}}, true},
		{"time shifted without delta", RouteChange{Kind: ChangeTimeShifted, Scope: ScopeVisit, BeforeVisitID: before, Message: "x"}, false},
		{"verification on visit scope", RouteChange{Kind: ChangeVerificationChanged, Scope: ScopeVisit, BeforeVisitID: before, Message: "x",
			Details: VerificationChange{Before: VerificationVerified, After: VerificationEstimated}}, false},
		{"verification on leg", RouteChange{Kind: ChangeVerificationChanged, Scope: ScopeLeg, LegPosition: intPtr(2), Message: "Estimated",
			Details: VerificationChange{Before: VerificationVerified, After: VerificationEstimated}}, true},
		{"cost in two currencies", RouteChange{Kind: ChangeCostChanged, Scope: ScopeRoute, Message: "x",
			Details: CostChange{Before: money(1), After: Money{AmountMinor: 1, Currency: "USD"}}}, false},
		{"route cost", RouteChange{Kind: ChangeCostChanged, Scope: ScopeRoute, Message: "Cheaper", Details: CostChange{Before: money(2), After: money(1)}}, true},
		{"participation without action", RouteChange{Kind: ChangeParticipationAction, Scope: ScopeVisit, AfterVisitID: after, Message: "x", Details: ParticipationAction{}}, false},
		{"details on kept", RouteChange{Kind: ChangeKept, Scope: ScopeVisit, BeforeVisitID: before, Message: "x", Details: TimeShift{Delta: time.Minute}}, false},
		{"route scope with visit", RouteChange{Kind: ChangeCostChanged, Scope: ScopeRoute, BeforeVisitID: before, Message: "x", Details: CostChange{Before: money(2), After: money(1)}}, false},
		{"removed on route scope", RouteChange{Kind: ChangeRemoved, Scope: ScopeRoute, Message: "x"}, false},
		{"visit scope without visits", RouteChange{Kind: ChangeTimeShifted, Scope: ScopeVisit, Message: "x", Details: TimeShift{Delta: time.Minute}}, false},
		{"leg scope at position zero", RouteChange{Kind: ChangeVerificationChanged, Scope: ScopeLeg, LegPosition: intPtr(0), Message: "x",
			Details: VerificationChange{Before: VerificationVerified, After: VerificationEstimated}}, false},
		{"time shift on leg scope", RouteChange{Kind: ChangeTimeShifted, Scope: ScopeLeg, LegPosition: intPtr(1), Message: "x", Details: TimeShift{Delta: time.Minute}}, false},
		{"route scope with leg", RouteChange{Kind: ChangeCostChanged, Scope: ScopeRoute, LegPosition: intPtr(1), Message: "x", Details: CostChange{Before: money(2), After: money(1)}}, false},
		{"visit scope with leg", RouteChange{Kind: ChangeRemoved, Scope: ScopeVisit, BeforeVisitID: before, LegPosition: intPtr(1), Message: "x"}, false},
		{"leg scope with visit", RouteChange{Kind: ChangeVerificationChanged, Scope: ScopeLeg, BeforeVisitID: before, LegPosition: intPtr(1), Message: "x",
			Details: VerificationChange{Before: VerificationVerified, After: VerificationEstimated}}, false},
		{"leg scope without position", RouteChange{Kind: ChangeVerificationChanged, Scope: ScopeLeg, Message: "x",
			Details: VerificationChange{Before: VerificationVerified, After: VerificationEstimated}}, false},
		{"removed without previous visit", RouteChange{Kind: ChangeRemoved, Scope: ScopeVisit, AfterVisitID: after, Message: "x"}, false},
		{"replaced without next visit", RouteChange{Kind: ChangeReplaced, Scope: ScopeVisit, BeforeVisitID: before, Message: "x"}, false},
		{"empty message", RouteChange{Kind: ChangeRemoved, Scope: ScopeVisit, BeforeVisitID: before}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.change.Validate(); (err == nil) != tt.ok {
				t.Fatalf("Validate() = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}
