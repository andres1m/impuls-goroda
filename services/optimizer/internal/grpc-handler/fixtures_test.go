package grpchandler

import (
	"time"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// The proto and domain fixtures below are written independently and describe the same
// data, so each mapping direction is checked against a hand-written expectation.

var day = time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)

func at(hour, minute int) time.Time {
	return day.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

func ts(hour, minute int) *timestamppb.Timestamp { return timestamppb.New(at(hour, minute)) }

func tptr(t time.Time) *time.Time { return &t }

func id(b byte) []byte {
	raw := make([]byte, 16)
	raw[0] = b
	return raw
}

func pbMoney(amount int64) *pb.Money { return &pb.Money{AmountMinor: amount, Currency: "RUB"} }

func money(amount int64) domain.Money { return domain.Money{AmountMinor: amount, Currency: "RUB"} }

func moneyPtr(amount int64) *domain.Money {
	m := money(amount)
	return &m
}

func pbProvenance() *pb.Provenance {
	return &pb.Provenance{
		SourceName:      "kudago",
		SourceUrl:       proto.String("https://example.org/event"),
		SourceRecordId:  id(7),
		SourceUpdatedAt: ts(6, 0),
		FetchedAt:       ts(7, 0),
		VerifiedAt:      ts(8, 0),
	}
}

func domainProvenance() domain.Provenance {
	return domain.Provenance{
		SourceName:      "kudago",
		SourceURL:       proto.String("https://example.org/event"),
		SourceRecordID:  &domain.SourceRecordID{7},
		SourceUpdatedAt: tptr(at(6, 0)),
		FetchedAt:       at(7, 0),
		VerifiedAt:      tptr(at(8, 0)),
	}
}

func pbFreeCost() *pb.CostSnapshot {
	return &pb.CostSnapshot{
		Price:          &pb.Price{Status: pb.PriceStatus_PRICE_STATUS_FREE, Currency: "RUB", LowerMinor: proto.Int64(0), UpperMinor: proto.Int64(0)},
		PersonalAmount: pbMoney(0),
		Provenance:     &pb.Provenance{SourceName: "optimizer", FetchedAt: ts(7, 0)},
	}
}

func domainFreeCost() domain.CostSnapshot {
	return domain.CostSnapshot{
		Price:          domain.Price{Status: domain.PriceFree, Currency: "RUB", LowerMinor: proto.Int64(0), UpperMinor: proto.Int64(0)},
		PersonalAmount: moneyPtr(0),
		Provenance:     domain.Provenance{SourceName: "optimizer", FetchedAt: at(7, 0)},
	}
}

func pbLeg(position uint32, from, to pb.LegEndpointKind, fromVisit, toVisit []byte, departure, arrival *timestamppb.Timestamp) *pb.RouteLeg {
	return &pb.RouteLeg{
		Position:       position,
		FromKind:       from,
		ToKind:         to,
		FromVisitId:    fromVisit,
		ToVisitId:      toVisit,
		DepartureAt:    departure,
		ArrivalAt:      arrival,
		Mode:           "walk",
		DistanceMeters: proto.Float64(800),
		Geometry:       []*pb.Coordinate{{Longitude: 37.6, Latitude: 55.7}, {Longitude: 37.61, Latitude: 55.71}},
		Verification:   pb.VerificationStatus_VERIFICATION_STATUS_ESTIMATED,
		Evidence: &pb.LegEvidence{
			Provider:    "optimizer",
			Method:      "distance_estimate",
			ObservedAt:  ts(7, 0),
			Mode:        "walk",
			Limitations: []string{"no live transit data"},
		},
		Cost: pbFreeCost(),
	}
}

func domainLeg(position int, from, to domain.LegEndpoint, fromVisit, toVisit *domain.VisitID, departure, arrival time.Time) domain.Leg {
	return domain.Leg{
		Position:       position,
		From:           from,
		To:             to,
		FromVisitID:    fromVisit,
		ToVisitID:      toVisit,
		DepartureAt:    departure,
		ArrivalAt:      arrival,
		Mode:           "walk",
		DistanceMeters: proto.Float64(800),
		Geometry:       []domain.Coordinate{{Longitude: 37.6, Latitude: 55.7}, {Longitude: 37.61, Latitude: 55.71}},
		Verification:   domain.VerificationEstimated,
		Evidence: domain.LegEvidence{
			Provider:    "optimizer",
			Method:      "distance_estimate",
			ObservedAt:  at(7, 0),
			Mode:        "walk",
			Limitations: []string{"no live transit data"},
		},
		Cost: domainFreeCost(),
	}
}

func pbPlan() *pb.RoutePlan {
	return &pb.RoutePlan{
		Archetype:       pb.Archetype_ARCHETYPE_URBAN_AVANTGARDE,
		StartAt:         ts(9, 0),
		EndAt:           ts(20, 0),
		Origin:          &pb.Coordinate{Longitude: 37.59, Latitude: 55.69},
		Destination:     &pb.Coordinate{Longitude: 37.62, Latitude: 55.72},
		CatalogRevision: 42,
		Result:          pb.ResultStatus_RESULT_STATUS_PARTIAL,
		Warnings: []*pb.Warning{
			{Code: "LATE_ENTRY_UNCONFIRMED", Scope: pb.TargetScope_TARGET_SCOPE_VISIT, VisitId: id(1), Message: "Late entry is not confirmed"},
			{Code: "ESTIMATED_TRANSIT", Scope: pb.TargetScope_TARGET_SCOPE_LEG, LegPosition: proto.Uint32(2), Message: "Transit time is estimated"},
		},
		Conflicts: []*pb.Conflict{{Code: "LUNCH_SHORTENED", VisitIds: [][]byte{id(2)}, SessionIds: [][]byte{id(3)}, Message: "Lunch is shorter"}},
		Cost: &pb.CostSummary{
			KnownPersonal:     pbMoney(50000),
			KnownTransport:    pbMoney(0),
			ProgramAmount:     pbMoney(20000),
			TotalLower:        pbMoney(50000),
			TotalUpper:        pbMoney(70000),
			UnknownComponents: []*pb.UnknownCostComponent{{Code: "BENEFIT_UNCONFIRMED", Message: "Benefit is not confirmed"}},
			BudgetConclusion:  pb.BudgetConclusion_BUDGET_CONCLUSION_UNKNOWN,
		},
		Geometry: []*pb.Coordinate{{Longitude: 37.59, Latitude: 55.69}, {Longitude: 37.62, Latitude: 55.72}},
		Steps: []*pb.RouteStep{
			{
				VisitId:            id(1),
				Kind:               pb.VisitKind_VISIT_KIND_VISIT,
				Position:           1,
				ArrivalAt:          ts(10, 0),
				VisitStartAt:       ts(10, 0),
				VisitEndAt:         ts(11, 0),
				DepartureAt:        ts(11, 0),
				MinDurationSeconds: 3600,
				Pinned:             true,
				Obligation:         true,
				Participation: &pb.ParticipationSnapshot{
					Status:   pb.ParticipationStatus_PARTICIPATION_STATUS_PROVIDER_CONFIRMED,
					Evidence: pb.ParticipationEvidence_PARTICIPATION_EVIDENCE_PROVIDER,
				},
				Catalog: &pb.CatalogSnapshot{
					PlaceId:             id(11),
					EntranceId:          id(12),
					EventId:             id(13),
					SessionId:           id(14),
					Title:               "Exhibition",
					Category:            pb.Category_CATEGORY_CULTURE,
					InterestMask:        0b1000000000001,
					Availability:        pb.AvailabilityStatus_AVAILABILITY_STATUS_REGISTRATION_REQUIRED,
					RegistrationDetails: "Register online",
					AgeRequirements:     "16+",
					SessionStartsAt:     ts(10, 0),
					SessionEndsAt:       ts(18, 0),
					SessionVersion:      "3",
					DataMode:            pb.DataMode_DATA_MODE_PREPARED,
					Provenance:          pbProvenance(),
				},
				Cost: &pb.CostSnapshot{
					PriceOfferId:      id(15),
					Audience:          "student",
					Price:             &pb.Price{Status: pb.PriceStatus_PRICE_STATUS_RANGE, Currency: "RUB", LowerMinor: proto.Int64(50000), UpperMinor: proto.Int64(70000)},
					PersonalAmount:    pbMoney(50000),
					ProgramAmount:     pbMoney(20000),
					UnknownComponents: []*pb.UnknownCostComponent{{Code: "BENEFIT_UNCONFIRMED", Message: "Benefit is not confirmed"}},
					Provenance:        pbProvenance(),
				},
				AppliedConstraints: []*pb.AppliedConstraint{{
					Code:     "FIXED_SESSION",
					Strength: pb.ConstraintStrength_CONSTRAINT_STRENGTH_HARD,
					Outcome:  pb.ConstraintOutcome_CONSTRAINT_OUTCOME_CONDITIONAL,
					Message:  "Starts with the session",
				}},
			},
			{
				VisitId:      id(2),
				Kind:         pb.VisitKind_VISIT_KIND_FREE_TIME,
				Position:     2,
				ArrivalAt:    ts(11, 20),
				VisitStartAt: ts(11, 20),
				VisitEndAt:   ts(12, 0),
				DepartureAt:  ts(12, 0),
				Participation: &pb.ParticipationSnapshot{
					Status:   pb.ParticipationStatus_PARTICIPATION_STATUS_NOT_REQUIRED,
					Evidence: pb.ParticipationEvidence_PARTICIPATION_EVIDENCE_NONE,
				},
			},
		},
		Legs: []*pb.RouteLeg{
			pbLeg(1, pb.LegEndpointKind_LEG_ENDPOINT_KIND_ORIGIN, pb.LegEndpointKind_LEG_ENDPOINT_KIND_VISIT, nil, id(1), ts(9, 40), ts(10, 0)),
			pbLeg(2, pb.LegEndpointKind_LEG_ENDPOINT_KIND_VISIT, pb.LegEndpointKind_LEG_ENDPOINT_KIND_VISIT, id(1), id(2), ts(11, 0), ts(11, 20)),
			pbLeg(3, pb.LegEndpointKind_LEG_ENDPOINT_KIND_VISIT, pb.LegEndpointKind_LEG_ENDPOINT_KIND_DESTINATION, id(2), nil, ts(12, 0), ts(12, 30)),
		},
	}
}

func domainPlan() domain.Plan {
	first, second := domain.VisitID{1}, domain.VisitID{2}
	legPosition := 2
	return domain.Plan{
		Archetype:       domain.ArchetypeUrbanAvantgarde,
		Start:           at(9, 0),
		End:             at(20, 0),
		Origin:          domain.Coordinate{Longitude: 37.59, Latitude: 55.69},
		Destination:     &domain.Coordinate{Longitude: 37.62, Latitude: 55.72},
		CatalogRevision: 42,
		Result:          domain.ResultPartial,
		Warnings: []domain.Warning{
			{Code: "LATE_ENTRY_UNCONFIRMED", Scope: domain.ScopeVisit, VisitID: &domain.VisitID{1}, Message: "Late entry is not confirmed"},
			{Code: "ESTIMATED_TRANSIT", Scope: domain.ScopeLeg, LegPosition: &legPosition, Message: "Transit time is estimated"},
		},
		Conflicts: []domain.Conflict{{Code: "LUNCH_SHORTENED", VisitIDs: []domain.VisitID{{2}}, SessionIDs: []domain.SessionID{{3}}, Message: "Lunch is shorter"}},
		Cost: domain.CostSummary{
			KnownPersonal:     money(50000),
			KnownTransport:    money(0),
			ProgramAmount:     money(20000),
			TotalLower:        moneyPtr(50000),
			TotalUpper:        moneyPtr(70000),
			UnknownComponents: []domain.UnknownCostComponent{{Code: "BENEFIT_UNCONFIRMED", Message: "Benefit is not confirmed"}},
			BudgetConclusion:  domain.BudgetUnknown,
		},
		Geometry: []domain.Coordinate{{Longitude: 37.59, Latitude: 55.69}, {Longitude: 37.62, Latitude: 55.72}},
		Steps: []domain.Step{
			{
				VisitID:      first,
				Kind:         domain.StepVisit,
				Position:     1,
				ArrivalAt:    at(10, 0),
				VisitStartAt: at(10, 0),
				VisitEndAt:   at(11, 0),
				DepartureAt:  at(11, 0),
				MinDuration:  time.Hour,
				Pinned:       true,
				Obligation:   true,
				Participation: domain.Participation{
					Status:   domain.ParticipationProviderConfirmed,
					Evidence: domain.EvidenceProvider,
				},
				Catalog: &domain.CatalogSnapshot{
					PlaceID:             domain.PlaceID{11},
					EntranceID:          &domain.EntranceID{12},
					EventID:             &domain.EventID{13},
					SessionID:           &domain.SessionID{14},
					Title:               "Exhibition",
					Category:            domain.CategoryCulture,
					InterestMask:        domain.Interests(domain.InterestContemporaryArt, domain.InterestCinema),
					Availability:        domain.AvailabilityRegistrationRequired,
					RegistrationDetails: "Register online",
					AgeRequirements:     "16+",
					SessionStart:        tptr(at(10, 0)),
					SessionEnd:          tptr(at(18, 0)),
					SessionVersion:      "3",
					DataMode:            domain.DataPrepared,
					Provenance:          domainProvenance(),
				},
				Cost: &domain.CostSnapshot{
					PriceOfferID:      &domain.PriceOfferID{15},
					Audience:          domain.AudienceStudent,
					Price:             domain.Price{Status: domain.PriceRange, Currency: "RUB", LowerMinor: proto.Int64(50000), UpperMinor: proto.Int64(70000)},
					PersonalAmount:    moneyPtr(50000),
					ProgramAmount:     moneyPtr(20000),
					UnknownComponents: []domain.UnknownCostComponent{{Code: "BENEFIT_UNCONFIRMED", Message: "Benefit is not confirmed"}},
					Provenance:        domainProvenance(),
				},
				AppliedConstraints: []domain.AppliedConstraint{{
					Code:     "FIXED_SESSION",
					Strength: domain.StrengthHard,
					Outcome:  domain.OutcomeConditional,
					Message:  "Starts with the session",
				}},
			},
			{
				VisitID:       second,
				Kind:          domain.StepFreeTime,
				Position:      2,
				ArrivalAt:     at(11, 20),
				VisitStartAt:  at(11, 20),
				VisitEndAt:    at(12, 0),
				DepartureAt:   at(12, 0),
				Participation: domain.Participation{Status: domain.ParticipationNotRequired, Evidence: domain.EvidenceNone},
			},
		},
		Legs: []domain.Leg{
			domainLeg(1, domain.EndpointOrigin, domain.EndpointVisit, nil, &domain.VisitID{1}, at(9, 40), at(10, 0)),
			domainLeg(2, domain.EndpointVisit, domain.EndpointVisit, &domain.VisitID{1}, &domain.VisitID{2}, at(11, 0), at(11, 20)),
			domainLeg(3, domain.EndpointVisit, domain.EndpointDestination, &domain.VisitID{2}, nil, at(12, 0), at(12, 30)),
		},
	}
}

func pbConstraints() *pb.RouteConstraints {
	return &pb.RouteConstraints{
		InterestMask:       0b1000000000001,
		ExcludedCategories: []pb.Category{pb.Category_CATEGORY_GASTRO, pb.Category_CATEGORY_SPORT},
		MovementModes:      []string{"walk", "transit"},
		LoadProfile:        "moderate",
		Budget:             &pb.Budget{Mode: pb.BudgetMode_BUDGET_MODE_STRICT, Limit: pbMoney(300000)},
		BenefitPrograms:    []string{"pushkin_card"},
		ProgramBalance:     &pb.ProgramBalance{Program: "pushkin_card", Balance: pbMoney(500000)},
		AudienceClaims:     []string{"student"},
		Obligations: []*pb.RouteObligation{
			{SessionId: id(14), StartsAt: ts(10, 0), ArrivalBufferSeconds: 900, Participation: pb.ParticipationStatus_PARTICIPATION_STATUS_USER_REPORTED_CONFIRMED},
			{VisitId: id(1), Participation: pb.ParticipationStatus_PARTICIPATION_STATUS_ACTION_REQUIRED},
		},
		SoftPreferences:  []string{"quiet places"},
		LunchWindow:      &pb.LunchWindow{StartAt: ts(13, 0), EndAt: ts(14, 30), MinDurationSeconds: 2700},
		AcceptedUnknowns: []string{"PRICE_UNKNOWN"},
	}
}

func domainConstraints() domain.RouteConstraints {
	return domain.RouteConstraints{
		InterestMask:       domain.Interests(domain.InterestContemporaryArt, domain.InterestCinema),
		ExcludedCategories: []domain.Category{domain.CategoryGastro, domain.CategorySport},
		MovementModes:      []domain.MovementMode{"walk", "transit"},
		LoadProfile:        "moderate",
		Budget:             domain.Budget{Mode: domain.BudgetStrict, Limit: moneyPtr(300000)},
		BenefitPrograms:    []string{"pushkin_card"},
		ProgramBalance:     &domain.ProgramBalance{Program: "pushkin_card", Balance: money(500000)},
		AudienceClaims:     []string{"student"},
		Obligations: []domain.Obligation{
			{SessionID: &domain.SessionID{14}, StartsAt: tptr(at(10, 0)), ArrivalBuffer: 15 * time.Minute, Participation: domain.ParticipationUserReported},
			{VisitID: &domain.VisitID{1}, Participation: domain.ParticipationActionRequired},
		},
		SoftPreferences:  []string{"quiet places"},
		LunchWindow:      &domain.LunchWindow{Start: at(13, 0), End: at(14, 30), MinDuration: 45 * time.Minute},
		AcceptedUnknowns: []string{"PRICE_UNKNOWN"},
	}
}

func pbOptimizeRequest() *pb.OptimizeRequest {
	return &pb.OptimizeRequest{
		City:        "moscow",
		Timezone:    "Europe/Moscow",
		StartAt:     ts(9, 0),
		EndAt:       ts(20, 0),
		Origin:      &pb.Coordinate{Longitude: 37.59, Latitude: 55.69},
		Destination: &pb.Coordinate{Longitude: 37.62, Latitude: 55.72},
		Constraints: pbConstraints(),
	}
}

func domainOptimizeRequest() domain.OptimizeRequest {
	return domain.OptimizeRequest{
		City:        "moscow",
		Timezone:    "Europe/Moscow",
		Start:       at(9, 0),
		End:         at(20, 0),
		Origin:      domain.Coordinate{Longitude: 37.59, Latitude: 55.69},
		Destination: &domain.Coordinate{Longitude: 37.62, Latitude: 55.72},
		Constraints: domainConstraints(),
	}
}

func pbRecomputeRequest() *pb.RecomputeRequest {
	return &pb.RecomputeRequest{
		City:        "moscow",
		Timezone:    "Europe/Moscow",
		BasePlan:    pbPlan(),
		Constraints: pbConstraints(),
		History: []*pb.VisitExecution{{
			VisitId:         id(1),
			Status:          pb.ExecutionStatus_EXECUTION_STATUS_COMPLETED,
			ActualStartedAt: ts(10, 5),
			ActualEndedAt:   ts(11, 0),
		}},
		Trigger: &pb.RecomputeRequest_Pin{Pin: &pb.PinTrigger{VisitId: id(2), Kind: pb.PinKind_PIN_KIND_OBLIGATION}},
	}
}

func domainRecomputeRequest() domain.RecomputeRequest {
	return domain.RecomputeRequest{
		City:        "moscow",
		Timezone:    "Europe/Moscow",
		Base:        domainPlan(),
		Constraints: domainConstraints(),
		History: []domain.VisitExecution{{
			VisitID:     domain.VisitID{1},
			Status:      domain.ExecutionCompleted,
			ActualStart: tptr(at(10, 5)),
			ActualEnd:   tptr(at(11, 0)),
		}},
		Trigger: domain.PinTrigger{VisitID: domain.VisitID{2}, Kind: domain.PinObligation},
	}
}
