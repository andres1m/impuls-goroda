package domain

import (
	"testing"
	"time"
)

func validFreshness() DataFreshness {
	return DataFreshness{DataMode: DataPrepared, DataAsOf: timePtr(fetched), CatalogRevision: 42}
}

func conflict() Conflict {
	return Conflict{Code: "SESSION_UNREACHABLE", SessionIDs: []SessionID{{3}}, Message: "Cannot reach the session"}
}

func validOptimizeResult() OptimizeResult {
	return OptimizeResult{
		Status:          ResultReady,
		Routes:          []Plan{validPlan()},
		Data:            validFreshness(),
		ComputationTime: 12 * time.Millisecond,
	}
}

func TestOptimizeResultValidate(t *testing.T) {
	second := validPlan()
	second.Archetype = ArchetypeHistoryHeritage
	third := validPlan()
	third.Archetype = ArchetypeActionSocial
	fourth := validPlan()
	fourth.Archetype = ArchetypeActionSocial

	tests := []struct {
		name   string
		mutate func(*OptimizeResult)
		ok     bool
	}{
		{"ready", func(*OptimizeResult) {}, true},
		{"three archetypes", func(r *OptimizeResult) { r.Routes = append(r.Routes, second, third) }, true},
		{"ready without routes", func(r *OptimizeResult) { r.Routes = nil }, false},
		{"four routes", func(r *OptimizeResult) { r.Routes = append(r.Routes, second, third, fourth) }, false},
		{"repeated archetype", func(r *OptimizeResult) { r.Routes = append(r.Routes, validPlan()) }, false},
		{"no route with route", func(r *OptimizeResult) { r.Status = ResultNoFeasibleRoute }, false},
		{"no route", func(r *OptimizeResult) { r.Status, r.Routes = ResultNoFeasibleRoute, nil }, true},
		{"conflict without conflict", func(r *OptimizeResult) { r.Status, r.Routes = ResultConflict, nil }, false},
		{"conflict", func(r *OptimizeResult) { r.Status, r.Routes, r.Conflicts = ResultConflict, nil, []Conflict{conflict()} }, true},
		{"invalid status", func(r *OptimizeResult) { r.Status = "DONE" }, false},
		{"invalid route", func(r *OptimizeResult) { r.Routes[0].Steps[0].Position = 7 }, false},
		{"invalid warning", func(r *OptimizeResult) { r.Warnings = []Warning{{Code: "x", Scope: ScopeRoute, Message: "x"}} }, false},
		{"route warning", func(r *OptimizeResult) {
			r.Warnings = []Warning{{Code: "FEWER_VARIANTS", Scope: ScopeRoute, Message: "Only one route"}}
		}, true},
		{"visit warning", func(r *OptimizeResult) {
			r.Warnings = []Warning{{Code: "LATE", Scope: ScopeVisit, VisitID: &VisitID{1}, Message: "x"}}
		}, false},
		{"invalid conflict", func(r *OptimizeResult) { r.Conflicts = []Conflict{{Code: "X"}} }, false},
		{"negative time", func(r *OptimizeResult) { r.ComputationTime = -time.Millisecond }, false},
		{"invalid data mode", func(r *OptimizeResult) { r.Data.DataMode = "" }, false},
		{"negative revision", func(r *OptimizeResult) { r.Data.CatalogRevision = -1 }, false},
		{"zero data time", func(r *OptimizeResult) { r.Data.DataAsOf = &time.Time{} }, false},
		{"no data time", func(r *OptimizeResult) { r.Data.DataAsOf = nil }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := validOptimizeResult()
			tt.mutate(&r)
			if err := r.Validate(); (err == nil) != tt.ok {
				t.Fatalf("Validate() = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

func validRecomputeResult() RecomputeResult {
	candidate := validPlan()
	return RecomputeResult{
		Status:    RecomputeProposed,
		Candidate: &candidate,
		Changes: []RouteChange{{
			Kind: ChangeTimeShifted, Scope: ScopeVisit, BeforeVisitID: &VisitID{1}, AfterVisitID: &VisitID{1},
			Message: "Later", Details: TimeShift{Delta: 10 * time.Minute},
		}},
		Data:            validFreshness(),
		ComputationTime: 5 * time.Millisecond,
	}
}

func TestRecomputeResultValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RecomputeResult)
		ok     bool
	}{
		{"proposed", func(*RecomputeResult) {}, true},
		{"proposed without candidate", func(r *RecomputeResult) { r.Candidate = nil }, false},
		{"unchanged", func(r *RecomputeResult) { r.Status, r.Candidate, r.Changes = RecomputeUnchanged, nil, nil }, true},
		{"unchanged with candidate", func(r *RecomputeResult) { r.Status, r.Changes = RecomputeUnchanged, nil }, false},
		{"unchanged with changes", func(r *RecomputeResult) { r.Status, r.Candidate = RecomputeUnchanged, nil }, false},
		{"conflict without conflict", func(r *RecomputeResult) { r.Status, r.Candidate = RecomputeConflict, nil }, false},
		{"conflict", func(r *RecomputeResult) {
			r.Status, r.Candidate, r.Conflicts = RecomputeConflict, nil, []Conflict{conflict()}
		}, true},
		{"invalid status", func(r *RecomputeResult) { r.Status = "applied" }, false},
		{"invalid candidate", func(r *RecomputeResult) { r.Candidate.Steps[0].Position = 7 }, false},
		{"invalid change", func(r *RecomputeResult) { r.Changes[0].Message = "" }, false},
		{"invalid conflict", func(r *RecomputeResult) { r.Conflicts = []Conflict{{Code: "X"}} }, false},
		{"negative time", func(r *RecomputeResult) { r.ComputationTime = -1 }, false},
		{"invalid data", func(r *RecomputeResult) { r.Data.DataMode = "" }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := validRecomputeResult()
			tt.mutate(&r)
			if err := r.Validate(); (err == nil) != tt.ok {
				t.Fatalf("Validate() = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}
