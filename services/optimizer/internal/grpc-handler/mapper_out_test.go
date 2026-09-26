package grpchandler

import (
	"math"
	"testing"
	"time"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"google.golang.org/protobuf/proto"
)

func TestPlanToProto(t *testing.T) {
	if got, want := planToProto(domainPlan()), pbPlan(); !proto.Equal(got, want) {
		t.Fatalf("mapped plan differs\n got: %v\nwant: %v", got, want)
	}
}

func TestPlanToProtoWithoutOptionalFields(t *testing.T) {
	p := domain.Plan{
		Archetype: domain.ArchetypeActionSocial,
		Start:     at(9, 0),
		End:       at(20, 0),
		Result:    domain.ResultNoFeasibleRoute,
		Cost: domain.CostSummary{
			KnownPersonal: money(0), KnownTransport: money(0), ProgramAmount: money(0),
			BudgetConclusion: domain.BudgetNotApplicable,
		},
	}
	want := &pb.RoutePlan{
		Archetype: pb.Archetype_ARCHETYPE_ACTION_SOCIAL,
		StartAt:   ts(9, 0),
		EndAt:     ts(20, 0),
		Origin:    &pb.Coordinate{},
		Result:    pb.ResultStatus_RESULT_STATUS_NO_FEASIBLE_ROUTE,
		Cost: &pb.CostSummary{
			KnownPersonal: pbMoney(0), KnownTransport: pbMoney(0), ProgramAmount: pbMoney(0),
			BudgetConclusion: pb.BudgetConclusion_BUDGET_CONCLUSION_NOT_APPLICABLE,
		},
	}
	if got := planToProto(p); !proto.Equal(got, want) {
		t.Fatalf("mapped plan differs\n got: %v\nwant: %v", got, want)
	}
}

func TestPlanRoundTrip(t *testing.T) {
	r := &reader{}
	back := planFromProto(r, "plan", planToProto(domainPlan()))
	if err := r.result(); err != nil {
		t.Fatal(err)
	}
	if got := planToProto(back); !proto.Equal(got, pbPlan()) {
		t.Fatalf("round trip changed the plan: %v", got)
	}
}

func TestOptimizeResponseToProto(t *testing.T) {
	result := domain.OptimizeResult{
		Status:          domain.ResultPartial,
		Routes:          []domain.Plan{domainPlan()},
		Warnings:        []domain.Warning{{Code: "FEWER_VARIANTS", Scope: domain.ScopeRoute, Message: "Only one route"}},
		Conflicts:       []domain.Conflict{{Code: "SESSION_UNREACHABLE", SessionIDs: []domain.SessionID{{3}}, Message: "x"}},
		Data:            domain.DataFreshness{DataMode: domain.DataSynthetic, DataAsOf: tptr(at(7, 0)), CatalogRevision: 42},
		ComputationTime: 1500 * time.Millisecond,
	}
	want := &pb.OptimizeResponse{
		Status:            pb.ResultStatus_RESULT_STATUS_PARTIAL,
		Routes:            []*pb.RoutePlan{pbPlan()},
		Warnings:          []*pb.Warning{{Code: "FEWER_VARIANTS", Scope: pb.TargetScope_TARGET_SCOPE_ROUTE, Message: "Only one route"}},
		Conflicts:         []*pb.Conflict{{Code: "SESSION_UNREACHABLE", SessionIds: [][]byte{id(3)}, Message: "x"}},
		Data:              &pb.DataFreshness{DataMode: pb.DataMode_DATA_MODE_SYNTHETIC, DataAsOf: ts(7, 0), CatalogRevision: 42},
		ComputationTimeMs: 1500,
	}
	if got := optimizeResponseToProto(result); !proto.Equal(got, want) {
		t.Fatalf("mapped response differs\n got: %v\nwant: %v", got, want)
	}
}

func TestRecomputeResponseToProto(t *testing.T) {
	candidate := domainPlan()
	legPosition := 2
	result := domain.RecomputeResult{
		Status:    domain.RecomputeProposed,
		Candidate: &candidate,
		Changes: []domain.RouteChange{
			{Kind: domain.ChangeTimeShifted, Scope: domain.ScopeVisit, BeforeVisitID: &domain.VisitID{1}, AfterVisitID: &domain.VisitID{1},
				Message: "Later", Details: domain.TimeShift{Delta: -20 * time.Minute}},
			{Kind: domain.ChangeCostChanged, Scope: domain.ScopeRoute, Message: "Cheaper", Details: domain.CostChange{Before: money(2), After: money(1)}},
			{Kind: domain.ChangeParticipationAction, Scope: domain.ScopeVisit, AfterVisitID: &domain.VisitID{2},
				Message: "Book", Details: domain.ParticipationAction{Action: "book_ticket"}},
			{Kind: domain.ChangeVerificationChanged, Scope: domain.ScopeLeg, LegPosition: &legPosition, Message: "Estimated",
				Details: domain.VerificationChange{Before: domain.VerificationVerified, After: domain.VerificationEstimated}},
			{Kind: domain.ChangeRemoved, Scope: domain.ScopeVisit, BeforeVisitID: &domain.VisitID{3}, Message: "Removed"},
		},
		Conflicts:       []domain.Conflict{{Code: "SESSION_UNREACHABLE", VisitIDs: []domain.VisitID{{3}}, Message: "x"}},
		Data:            domain.DataFreshness{DataMode: domain.DataLive, CatalogRevision: 43},
		ComputationTime: 3 * time.Millisecond,
	}
	want := &pb.RecomputeResponse{
		Status:    pb.RecomputeStatus_RECOMPUTE_STATUS_PROPOSED,
		Candidate: pbPlan(),
		Changes: []*pb.RouteChange{
			{Kind: pb.RouteChangeKind_ROUTE_CHANGE_KIND_TIME_SHIFTED, Scope: pb.TargetScope_TARGET_SCOPE_VISIT, BeforeVisitId: id(1), AfterVisitId: id(1),
				Message: "Later", Details: &pb.RouteChange_TimeShiftSeconds{TimeShiftSeconds: -1200}},
			{Kind: pb.RouteChangeKind_ROUTE_CHANGE_KIND_COST_CHANGED, Scope: pb.TargetScope_TARGET_SCOPE_ROUTE, Message: "Cheaper",
				Details: &pb.RouteChange_Cost{Cost: &pb.CostChange{Before: pbMoney(2), After: pbMoney(1)}}},
			{Kind: pb.RouteChangeKind_ROUTE_CHANGE_KIND_PARTICIPATION_ACTION, Scope: pb.TargetScope_TARGET_SCOPE_VISIT, AfterVisitId: id(2),
				Message: "Book", Details: &pb.RouteChange_ParticipationAction{ParticipationAction: "book_ticket"}},
			{Kind: pb.RouteChangeKind_ROUTE_CHANGE_KIND_VERIFICATION_CHANGED, Scope: pb.TargetScope_TARGET_SCOPE_LEG, LegPosition: proto.Uint32(2),
				Message: "Estimated", Details: &pb.RouteChange_Verification{Verification: &pb.VerificationChange{
					Before: pb.VerificationStatus_VERIFICATION_STATUS_VERIFIED, After: pb.VerificationStatus_VERIFICATION_STATUS_ESTIMATED,
				}}},
			{Kind: pb.RouteChangeKind_ROUTE_CHANGE_KIND_REMOVED, Scope: pb.TargetScope_TARGET_SCOPE_VISIT, BeforeVisitId: id(3), Message: "Removed"},
		},
		Conflicts:         []*pb.Conflict{{Code: "SESSION_UNREACHABLE", VisitIds: [][]byte{id(3)}, Message: "x"}},
		Data:              &pb.DataFreshness{DataMode: pb.DataMode_DATA_MODE_LIVE, CatalogRevision: 43},
		ComputationTimeMs: 3,
	}
	if got := recomputeResponseToProto(result); !proto.Equal(got, want) {
		t.Fatalf("mapped response differs\n got: %v\nwant: %v", got, want)
	}
}

func TestRecomputeResponseWithoutCandidate(t *testing.T) {
	got := recomputeResponseToProto(domain.RecomputeResult{
		Status: domain.RecomputeUnchanged,
		Data:   domain.DataFreshness{DataMode: domain.DataPrepared},
	})
	want := &pb.RecomputeResponse{
		Status: pb.RecomputeStatus_RECOMPUTE_STATUS_UNCHANGED,
		Data:   &pb.DataFreshness{DataMode: pb.DataMode_DATA_MODE_PREPARED},
	}
	if !proto.Equal(got, want) {
		t.Fatalf("mapped response differs\n got: %v\nwant: %v", got, want)
	}
}

func TestComputationMilliseconds(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want uint32
	}{
		{1500 * time.Millisecond, 1500},
		{999 * time.Microsecond, 0},
		{-time.Second, 0},
		{time.Duration(math.MaxInt64), math.MaxUint32},
		{time.Duration(math.MaxUint32) * time.Millisecond, math.MaxUint32},
		{time.Duration(math.MaxUint32-1) * time.Millisecond, math.MaxUint32 - 1},
	}
	for _, tt := range tests {
		if got := milliseconds(tt.in); got != tt.want {
			t.Errorf("milliseconds(%s) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
