package grpchandler

import (
	"errors"
	"math"
	"reflect"
	"testing"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestOptimizeRequestFromProto(t *testing.T) {
	got, err := optimizeRequestFromProto(pbOptimizeRequest())
	if err != nil {
		t.Fatal(err)
	}
	if want := domainOptimizeRequest(); !reflect.DeepEqual(got, want) {
		t.Fatalf("mapped request differs\n got: %+v\nwant: %+v", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("fixture must be valid: %v", err)
	}
}

func TestOptimizeRequestFromProtoWithoutOptionalFields(t *testing.T) {
	in := &pb.OptimizeRequest{
		City:     "perm",
		Timezone: "Asia/Yekaterinburg",
		StartAt:  ts(9, 0),
		EndAt:    ts(20, 0),
		Origin:   &pb.Coordinate{Longitude: 56.2, Latitude: 58.0},
		Constraints: &pb.RouteConstraints{
			MovementModes: []string{"walk"},
			LoadProfile:   "light",
			Budget:        &pb.Budget{Mode: pb.BudgetMode_BUDGET_MODE_NONE},
		},
	}
	got, err := optimizeRequestFromProto(in)
	if err != nil {
		t.Fatal(err)
	}
	want := domain.OptimizeRequest{
		City:     "perm",
		Timezone: "Asia/Yekaterinburg",
		Start:    at(9, 0),
		End:      at(20, 0),
		Origin:   domain.Coordinate{Longitude: 56.2, Latitude: 58.0},
		Constraints: domain.RouteConstraints{
			MovementModes: []domain.MovementMode{"walk"},
			LoadProfile:   "light",
			Budget:        domain.Budget{Mode: domain.BudgetNone},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mapped request differs\n got: %+v\nwant: %+v", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRecomputeRequestFromProto(t *testing.T) {
	tests := []struct {
		name    string
		trigger any
		want    domain.Trigger
	}{
		{"pin", &pb.RecomputeRequest_Pin{Pin: &pb.PinTrigger{VisitId: id(2), Kind: pb.PinKind_PIN_KIND_OBLIGATION}},
			domain.PinTrigger{VisitID: domain.VisitID{2}, Kind: domain.PinObligation}},
		{"delay", &pb.RecomputeRequest_Delay{Delay: &pb.DelayTrigger{
			Mode: pb.DelayMode_DELAY_MODE_FUTURE_WAIT, EffectiveStartAt: ts(11, 30),
			Position: &pb.Coordinate{Longitude: 37.6, Latitude: 55.7}, PositionSource: pb.PositionSource_POSITION_SOURCE_MANUAL,
		}}, domain.DelayTrigger{
			Mode: domain.DelayFutureWait, EffectiveStart: at(11, 30),
			Position: domain.Coordinate{Longitude: 37.6, Latitude: 55.7}, PositionSource: domain.PositionManual,
		}},
		{"cancellation", &pb.RecomputeRequest_Cancellation{Cancellation: &pb.CancellationTrigger{VisitIds: [][]byte{id(1), id(2)}, MinCatalogRevision: 43}},
			domain.CancellationTrigger{VisitIDs: []domain.VisitID{{1}, {2}}, MinCatalogRevision: 43}},
		{"removal", &pb.RecomputeRequest_Removal{Removal: &pb.RemovalTrigger{VisitId: id(2), Mode: pb.RemovalMode_REMOVAL_MODE_FREE_TIME}},
			domain.RemovalTrigger{VisitID: domain.VisitID{2}, Mode: domain.RemovalFreeTime}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := pbRecomputeRequest()
			switch trigger := tt.trigger.(type) {
			case *pb.RecomputeRequest_Pin:
				in.Trigger = trigger
			case *pb.RecomputeRequest_Delay:
				in.Trigger = trigger
			case *pb.RecomputeRequest_Cancellation:
				in.Trigger = trigger
			case *pb.RecomputeRequest_Removal:
				in.Trigger = trigger
			}
			got, err := recomputeRequestFromProto(in)
			if err != nil {
				t.Fatal(err)
			}
			want := domainRecomputeRequest()
			want.Trigger = tt.want
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("mapped request differs\n got: %+v\nwant: %+v", got, want)
			}
			if err := got.Validate(); err != nil {
				t.Fatalf("fixture must be valid: %v", err)
			}
		})
	}
}

func TestRequestFieldErrors(t *testing.T) {
	optimize := func(mutate func(*pb.OptimizeRequest)) error {
		in := pbOptimizeRequest()
		mutate(in)
		_, err := optimizeRequestFromProto(in)
		return err
	}
	recompute := func(mutate func(*pb.RecomputeRequest)) error {
		in := pbRecomputeRequest()
		mutate(in)
		_, err := recomputeRequestFromProto(in)
		return err
	}
	tests := []struct {
		name  string
		err   error
		field string
	}{
		{"short id", optimize(func(r *pb.OptimizeRequest) { r.Constraints.Obligations[0].SessionId = make([]byte, 15) }), "constraints.obligations[0].session_id"},
		{"long id", optimize(func(r *pb.OptimizeRequest) { r.Constraints.Obligations[1].VisitId = make([]byte, 17) }), "constraints.obligations[1].visit_id"},
		{"unspecified enum", optimize(func(r *pb.OptimizeRequest) { r.Constraints.Budget.Mode = pb.BudgetMode_BUDGET_MODE_UNSPECIFIED }), "constraints.budget.mode"},
		{"unknown enum", optimize(func(r *pb.OptimizeRequest) { r.Constraints.ExcludedCategories[1] = pb.Category(99) }), "constraints.excluded_categories[1]"},
		{"invalid timestamp", optimize(func(r *pb.OptimizeRequest) { r.StartAt = &timestamppb.Timestamp{Nanos: -1} }), "start_at"},
		{"missing timestamp", optimize(func(r *pb.OptimizeRequest) { r.EndAt = nil }), "end_at"},
		{"missing origin", optimize(func(r *pb.OptimizeRequest) { r.Origin = nil }), "origin"},
		{"missing constraints", optimize(func(r *pb.OptimizeRequest) { r.Constraints = nil }), "constraints"},
		{"missing budget", optimize(func(r *pb.OptimizeRequest) { r.Constraints.Budget = nil }), "constraints.budget"},
		{"missing balance amount", optimize(func(r *pb.OptimizeRequest) { r.Constraints.ProgramBalance.Balance = nil }), "constraints.program_balance.balance"},
		{"seconds overflow", optimize(func(r *pb.OptimizeRequest) { r.Constraints.LunchWindow.MinDurationSeconds = math.MaxInt64 }),
			"constraints.lunch_window.min_duration_seconds"},
		{"negative seconds overflow", optimize(func(r *pb.OptimizeRequest) { r.Constraints.Obligations[0].ArrivalBufferSeconds = math.MinInt64 }),
			"constraints.obligations[0].arrival_buffer_seconds"},
		{"lunch without end", optimize(func(r *pb.OptimizeRequest) { r.Constraints.LunchWindow.EndAt = nil }), "constraints.lunch_window.end_at"},
		{"obligation bad start", optimize(func(r *pb.OptimizeRequest) {
			r.Constraints.Obligations[0].StartsAt = &timestamppb.Timestamp{Nanos: 2e9}
		}),
			"constraints.obligations[0].starts_at"},
		{"obligation participation", optimize(func(r *pb.OptimizeRequest) { r.Constraints.Obligations[1].Participation = 0 }), "constraints.obligations[1].participation"},
		{"missing trigger", recompute(func(r *pb.RecomputeRequest) { r.Trigger = nil }), "trigger"},
		{"missing base plan", recompute(func(r *pb.RecomputeRequest) { r.BasePlan = nil }), "base_plan"},
		{"missing constraints", recompute(func(r *pb.RecomputeRequest) { r.Constraints = nil }), "constraints"},
		{"empty step visit", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Steps[0].VisitId = nil }), "base_plan.steps[0].visit_id"},
		{"step kind", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Steps[1].Kind = 0 }), "base_plan.steps[1].kind"},
		{"step participation", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Steps[1].Participation = nil }), "base_plan.steps[1].participation"},
		{"step evidence", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Steps[0].Participation.Evidence = 0 }), "base_plan.steps[0].participation.evidence"},
		{"step time", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Steps[0].DepartureAt = nil }), "base_plan.steps[0].departure_at"},
		{"step minimum", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Steps[0].MinDurationSeconds = math.MaxInt64 }), "base_plan.steps[0].min_duration_seconds"},
		{"catalog place", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Steps[0].Catalog.PlaceId = nil }), "base_plan.steps[0].catalog.place_id"},
		{"catalog entrance", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Steps[0].Catalog.EntranceId = id(1)[:3] }), "base_plan.steps[0].catalog.entrance_id"},
		{"catalog category", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Steps[0].Catalog.Category = 0 }), "base_plan.steps[0].catalog.category"},
		{"catalog availability", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Steps[0].Catalog.Availability = 0 }), "base_plan.steps[0].catalog.availability"},
		{"catalog data mode", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Steps[0].Catalog.DataMode = 0 }), "base_plan.steps[0].catalog.data_mode"},
		{"catalog session end", recompute(func(r *pb.RecomputeRequest) {
			r.BasePlan.Steps[0].Catalog.SessionEndsAt = &timestamppb.Timestamp{Seconds: math.MaxInt64}
		}),
			"base_plan.steps[0].catalog.session_ends_at"},
		{"catalog provenance", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Steps[0].Catalog.Provenance = nil }), "base_plan.steps[0].catalog.provenance"},
		{"provenance fetch time", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Steps[0].Catalog.Provenance.FetchedAt = nil }),
			"base_plan.steps[0].catalog.provenance.fetched_at"},
		{"provenance record", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Steps[0].Catalog.Provenance.SourceRecordId = []byte{1} }),
			"base_plan.steps[0].catalog.provenance.source_record_id"},
		{"cost price", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Steps[0].Cost.Price = nil }), "base_plan.steps[0].cost.price"},
		{"cost price status", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Steps[0].Cost.Price.Status = 0 }), "base_plan.steps[0].cost.price.status"},
		{"cost offer", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Steps[0].Cost.PriceOfferId = []byte{1} }), "base_plan.steps[0].cost.price_offer_id"},
		{"applied strength", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Steps[0].AppliedConstraints[0].Strength = 0 }),
			"base_plan.steps[0].applied_constraints[0].strength"},
		{"applied outcome", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Steps[0].AppliedConstraints[0].Outcome = 0 }),
			"base_plan.steps[0].applied_constraints[0].outcome"},
		{"leg endpoint", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Legs[2].ToKind = 0 }), "base_plan.legs[2].to_kind"},
		{"leg source", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Legs[1].FromKind = 0 }), "base_plan.legs[1].from_kind"},
		{"leg visit", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Legs[1].FromVisitId = []byte{1} }), "base_plan.legs[1].from_visit_id"},
		{"leg target visit", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Legs[1].ToVisitId = []byte{1} }), "base_plan.legs[1].to_visit_id"},
		{"leg departure", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Legs[0].DepartureAt = nil }), "base_plan.legs[0].departure_at"},
		{"leg arrival", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Legs[0].ArrivalAt = nil }), "base_plan.legs[0].arrival_at"},
		{"leg verification", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Legs[0].Verification = 0 }), "base_plan.legs[0].verification"},
		{"leg evidence", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Legs[0].Evidence = nil }), "base_plan.legs[0].evidence"},
		{"leg evidence time", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Legs[0].Evidence.ObservedAt = nil }), "base_plan.legs[0].evidence.observed_at"},
		{"leg cost", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Legs[0].Cost = nil }), "base_plan.legs[0].cost"},
		{"leg cost provenance", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Legs[0].Cost.Provenance = nil }), "base_plan.legs[0].cost.provenance"},
		{"plan archetype", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Archetype = 0 }), "base_plan.archetype"},
		{"plan result", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Result = 0 }), "base_plan.result"},
		{"plan start", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.StartAt = nil }), "base_plan.start_at"},
		{"plan end", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.EndAt = nil }), "base_plan.end_at"},
		{"plan origin", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Origin = nil }), "base_plan.origin"},
		{"plan cost", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Cost = nil }), "base_plan.cost"},
		{"summary personal", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Cost.KnownPersonal = nil }), "base_plan.cost.known_personal"},
		{"summary transport", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Cost.KnownTransport = nil }), "base_plan.cost.known_transport"},
		{"summary program", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Cost.ProgramAmount = nil }), "base_plan.cost.program_amount"},
		{"summary conclusion", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Cost.BudgetConclusion = 0 }), "base_plan.cost.budget_conclusion"},
		{"warning scope", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Warnings[1].Scope = 0 }), "base_plan.warnings[1].scope"},
		{"warning visit", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Warnings[0].VisitId = []byte{1} }), "base_plan.warnings[0].visit_id"},
		{"conflict visit", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Conflicts[0].VisitIds[0] = nil }), "base_plan.conflicts[0].visit_ids[0]"},
		{"conflict session", recompute(func(r *pb.RecomputeRequest) { r.BasePlan.Conflicts[0].SessionIds[0] = []byte{1} }), "base_plan.conflicts[0].session_ids[0]"},
		{"history visit", recompute(func(r *pb.RecomputeRequest) { r.History[0].VisitId = nil }), "history[0].visit_id"},
		{"history status", recompute(func(r *pb.RecomputeRequest) { r.History[0].Status = 0 }), "history[0].status"},
		{"history start", recompute(func(r *pb.RecomputeRequest) { r.History[0].ActualStartedAt = &timestamppb.Timestamp{Nanos: -1} }), "history[0].actual_started_at"},
		{"history end", recompute(func(r *pb.RecomputeRequest) { r.History[0].ActualEndedAt = &timestamppb.Timestamp{Nanos: -1} }), "history[0].actual_ended_at"},
		{"pin visit", recompute(func(r *pb.RecomputeRequest) {
			r.Trigger = &pb.RecomputeRequest_Pin{Pin: &pb.PinTrigger{Kind: pb.PinKind_PIN_KIND_NONE}}
		}), "pin.visit_id"},
		{"pin kind", recompute(func(r *pb.RecomputeRequest) {
			r.Trigger = &pb.RecomputeRequest_Pin{Pin: &pb.PinTrigger{VisitId: id(2)}}
		}), "pin.kind"},
		{"removal visit", recompute(func(r *pb.RecomputeRequest) {
			r.Trigger = &pb.RecomputeRequest_Removal{Removal: &pb.RemovalTrigger{Mode: pb.RemovalMode_REMOVAL_MODE_REBUILD}}
		}), "removal.visit_id"},
		{"removal mode", recompute(func(r *pb.RecomputeRequest) {
			r.Trigger = &pb.RecomputeRequest_Removal{Removal: &pb.RemovalTrigger{VisitId: id(2)}}
		}), "removal.mode"},
		{"cancellation visit", recompute(func(r *pb.RecomputeRequest) {
			r.Trigger = &pb.RecomputeRequest_Cancellation{Cancellation: &pb.CancellationTrigger{VisitIds: [][]byte{id(1), {9}}}}
		}), "cancellation.visit_ids[1]"},
		{"delay mode", recompute(func(r *pb.RecomputeRequest) {
			r.Trigger = &pb.RecomputeRequest_Delay{Delay: &pb.DelayTrigger{EffectiveStartAt: ts(11, 0), Position: &pb.Coordinate{}, PositionSource: 1}}
		}), "delay.mode"},
		{"delay start", recompute(func(r *pb.RecomputeRequest) {
			r.Trigger = &pb.RecomputeRequest_Delay{Delay: &pb.DelayTrigger{Mode: 1, Position: &pb.Coordinate{}, PositionSource: 1}}
		}), "delay.effective_start_at"},
		{"delay position", recompute(func(r *pb.RecomputeRequest) {
			r.Trigger = &pb.RecomputeRequest_Delay{Delay: &pb.DelayTrigger{Mode: 1, EffectiveStartAt: ts(11, 0), PositionSource: 1}}
		}), "delay.position"},
		{"delay source", recompute(func(r *pb.RecomputeRequest) {
			r.Trigger = &pb.RecomputeRequest_Delay{Delay: &pb.DelayTrigger{Mode: 1, EffectiveStartAt: ts(11, 0), Position: &pb.Coordinate{}}}
		}), "delay.position_source"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fe *fieldError
			if !errors.As(tt.err, &fe) {
				t.Fatalf("error = %v, want a field error", tt.err)
			}
			if fe.Field != tt.field {
				t.Fatalf("field = %q (%s), want %q", fe.Field, fe.Reason, tt.field)
			}
		})
	}
}

func TestFirstFieldErrorWins(t *testing.T) {
	in := pbOptimizeRequest()
	in.StartAt, in.EndAt = nil, nil
	_, err := optimizeRequestFromProto(in)
	var fe *fieldError
	if !errors.As(err, &fe) || fe.Field != "start_at" || fe.Reason != reasonRequired {
		t.Fatalf("error = %v, want start_at is required", err)
	}
	if fe.Error() != "start_at: is required" {
		t.Fatalf("Error() = %q", fe.Error())
	}
}

func TestMissingIDIsReportedAsRequired(t *testing.T) {
	in := pbRecomputeRequest()
	in.BasePlan.Steps[0].VisitId = nil
	_, err := recomputeRequestFromProto(in)
	var fe *fieldError
	if !errors.As(err, &fe) || fe.Reason != reasonRequired {
		t.Fatalf("error = %v, want %q", err, reasonRequired)
	}
	in = pbRecomputeRequest()
	in.BasePlan.Steps[0].VisitId = []byte{1}
	_, err = recomputeRequestFromProto(in)
	if !errors.As(err, &fe) || fe.Reason != "must be 16 bytes" {
		t.Fatalf("error = %v, want a length error", err)
	}
}

func TestMappingKeepsAbsentListsNil(t *testing.T) {
	r := &reader{}
	if idList[domain.VisitID](r, "ids", nil) != nil || movementModes(nil) != nil || coordinates(nil) != nil ||
		enumList(r, "categories", categories, nil) != nil || idListBytes[domain.VisitID](nil) != nil {
		t.Fatal("absent list mapped to an empty one")
	}
	if got := idList[domain.VisitID](r, "ids", [][]byte{}); got == nil || len(got) != 0 {
		t.Fatalf("empty list mapped to %v", got)
	}
	if r.result() != nil {
		t.Fatal(r.result())
	}
}
