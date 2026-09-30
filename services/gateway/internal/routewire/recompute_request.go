package routewire

import (
	"errors"
	"math"
	"strings"
	"time"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var ErrInvalidRecomputeInput = errors.New("invalid recompute input")

func BuildPinRecompute(routeID d.RouteID, city string, base d.RoutePlanSnapshot, history []d.Execution, visitID d.VisitID, input PinVisitInput) (*pb.RecomputeRequest, error) {
	request, err := buildRecomputeBase(routeID, city, base, history)
	if err != nil {
		return nil, err
	}
	found := false
	for _, step := range base.Steps {
		if step.VisitID == visitID && step.Kind == d.VisitPlace {
			found = true
			break
		}
	}
	if !found {
		return nil, ErrInvalidRecomputeInput
	}
	trigger, err := input.Proto(visitID)
	if err != nil {
		return nil, err
	}
	request.Trigger = &pb.RecomputeRequest_Pin{Pin: trigger}
	return request, nil
}

func buildRecomputeBase(routeID d.RouteID, city string, base d.RoutePlanSnapshot, history []d.Execution) (*pb.RecomputeRequest, error) {
	if routeID == (d.RouteID{}) || base.Validate() != nil || !connectedPlan(base) || len(base.Conflicts) != 0 || (base.Result != d.ResultReady && base.Result != d.ResultPartial) {
		return nil, ErrInvalidRecomputeInput
	}
	base = base.Clone()
	visits := make(map[d.VisitID]d.VisitKind, len(base.Steps))
	for i, step := range base.Steps {
		if step.Position != i+1 {
			return nil, ErrInvalidRecomputeInput
		}
		visits[step.VisitID] = step.Kind
	}
	for i, leg := range base.Legs {
		if leg.Position != i+1 {
			return nil, ErrInvalidRecomputeInput
		}
	}
	confirmed := ConfirmedRouteInput{City: city, Timezone: base.Timezone, StartAt: base.StartAt, EndAt: base.EndAt, Origin: coordinateWire(base.Origin), Constraints: constraintsWire(base.Constraints)}
	if base.Destination != nil {
		point := coordinateWire(*base.Destination)
		confirmed.Destination = &point
	}
	optimize, err := confirmed.Proto()
	if err != nil {
		return nil, err
	}
	optimize.Constraints.SemanticQuery = base.Constraints.SemanticQuery
	encoder := planEncoder{}
	request := &pb.RecomputeRequest{City: optimize.City, Timezone: optimize.Timezone, BasePlan: encoder.plan(base), Constraints: optimize.Constraints}
	seen := make(map[d.VisitID]bool, len(history))
	for _, value := range history {
		if value.Validate() != nil || value.RouteID != routeID || seen[value.VisitID] {
			return nil, ErrInvalidRecomputeInput
		}
		if _, ok := visits[value.VisitID]; !ok {
			return nil, ErrInvalidRecomputeInput
		}
		seen[value.VisitID] = true
		if value.Status == d.ExecutionPlanned {
			if value.ActualStartedAt != nil || value.ActualEndedAt != nil {
				return nil, ErrInvalidRecomputeInput
			}
			continue
		}
		if value.Status == d.ExecutionCompleted && (value.ActualStartedAt == nil || value.ActualEndedAt == nil) {
			return nil, ErrInvalidRecomputeInput
		}
		if value.ActualStartedAt != nil && value.ActualEndedAt != nil && !value.ActualEndedAt.After(*value.ActualStartedAt) {
			return nil, ErrInvalidRecomputeInput
		}
		request.History = append(request.History, &pb.VisitExecution{VisitId: rawID(&value.VisitID), Status: pb.ExecutionStatus(encoder.enum(string(value.Status), pb.ExecutionStatus_value, "EXECUTION_STATUS_")), ActualStartedAt: encoder.optionalTime(value.ActualStartedAt), ActualEndedAt: encoder.optionalTime(value.ActualEndedAt)})
	}
	if encoder.err != nil {
		return nil, encoder.err
	}
	return request, nil
}

type planEncoder struct{ err error }

func (e *planEncoder) enum(value string, values map[string]int32, prefix string) int32 {
	number, ok := values[prefix+strings.ToUpper(value)]
	if !ok || number == 0 {
		e.err = ErrInvalidRecomputeInput
	}
	return number
}

func (e *planEncoder) position(value int) uint32 {
	if value <= 0 || uint64(value) > math.MaxUint32 {
		e.err = ErrInvalidRecomputeInput
		return 0
	}
	return uint32(value)
}

func (e *planEncoder) instant(value time.Time) *timestamppb.Timestamp {
	out := timestamppb.New(value)
	if value.IsZero() || out.CheckValid() != nil {
		e.err = ErrInvalidRecomputeInput
	}
	return out
}

func (e *planEncoder) optionalTime(value *time.Time) *timestamppb.Timestamp {
	if value == nil {
		return nil
	}
	return e.instant(*value)
}

func rawID[T ~[16]byte](value *T) []byte {
	if value == nil {
		return nil
	}
	id := *value
	return append([]byte(nil), id[:]...)
}

func planCoordinates(values []d.Coordinate) []*pb.Coordinate {
	out := make([]*pb.Coordinate, 0, len(values))
	for _, value := range values {
		out = append(out, coordinateProto(coordinateWire(value)))
	}
	return out
}

func internalMoney(value *d.Money) *pb.Money {
	if value == nil {
		return nil
	}
	return &pb.Money{AmountMinor: value.AmountMinor, Currency: value.Currency}
}

func internalUnknowns(values []d.UnknownCostComponent) []*pb.UnknownCostComponent {
	out := make([]*pb.UnknownCostComponent, 0, len(values))
	for _, value := range values {
		out = append(out, &pb.UnknownCostComponent{Code: value.Code, Message: value.Message})
	}
	return out
}

func (e *planEncoder) provenance(value d.FactProvenance) *pb.Provenance {
	return &pb.Provenance{SourceName: value.SourceName, SourceUrl: value.SourceURL, SourceRecordId: rawID(value.SourceRecordID), SourceUpdatedAt: e.optionalTime(value.SourceUpdatedAt), FetchedAt: e.instant(value.FetchedAt), VerifiedAt: e.optionalTime(value.VerifiedAt)}
}

func (e *planEncoder) cost(value *d.CostSnapshot) *pb.CostSnapshot {
	if value == nil {
		return nil
	}
	return &pb.CostSnapshot{PriceOfferId: rawID(value.PriceOfferID), Audience: value.Audience, Price: &pb.Price{Status: pb.PriceStatus(e.enum(string(value.Price.Status), pb.PriceStatus_value, "PRICE_STATUS_")), Currency: value.Price.Currency, LowerMinor: value.Price.LowerMinor, UpperMinor: value.Price.UpperMinor}, PersonalAmount: internalMoney(value.PersonalAmount), ProgramAmount: internalMoney(value.ProgramAmount), UnknownComponents: internalUnknowns(value.UnknownComponents), Provenance: e.provenance(value.Provenance)}
}

func (e *planEncoder) catalog(value *d.CatalogSnapshot) *pb.CatalogSnapshot {
	if value == nil {
		return nil
	}
	category := e.enum(value.Category, pb.Category_value, "CATEGORY_")
	return &pb.CatalogSnapshot{PlaceId: rawID(value.PlaceID), EntranceId: rawID(value.EntranceID), EventId: rawID(value.EventID), SessionId: rawID(value.SessionID), Title: value.Title, Category: pb.Category(category), InterestMask: value.InterestMask, Availability: pb.AvailabilityStatus(e.enum(string(value.Availability), pb.AvailabilityStatus_value, "AVAILABILITY_STATUS_")), RegistrationDetails: value.RegistrationDetails, AgeRequirements: value.AgeRequirements, SessionStartsAt: e.optionalTime(value.SessionStartsAt), SessionEndsAt: e.optionalTime(value.SessionEndsAt), SessionVersion: value.SessionVersion, DataMode: pb.DataMode(e.enum(string(value.DataMode), pb.DataMode_value, "DATA_MODE_")), Provenance: e.provenance(value.Provenance)}
}

func (e *planEncoder) plan(value d.RoutePlanSnapshot) *pb.RoutePlan {
	cost := value.Cost
	out := &pb.RoutePlan{Archetype: pb.Archetype(e.enum(value.ArchetypeID, pb.Archetype_value, "ARCHETYPE_")), StartAt: e.instant(value.StartAt), EndAt: e.instant(value.EndAt), Origin: coordinateProto(coordinateWire(value.Origin)), CatalogRevision: int64(value.CatalogRevision), Result: pb.ResultStatus(e.enum(string(value.Result), pb.ResultStatus_value, "RESULT_STATUS_")), Geometry: planCoordinates(value.Geometry), Cost: &pb.CostSummary{KnownPersonal: internalMoney(&cost.KnownPersonal), KnownTransport: internalMoney(&cost.KnownTransport), ProgramAmount: internalMoney(&cost.ProgramAmount), TotalLower: internalMoney(cost.TotalLower), TotalUpper: internalMoney(cost.TotalUpper), UnknownComponents: internalUnknowns(cost.UnknownComponents), BudgetConclusion: pb.BudgetConclusion(e.enum(string(cost.BudgetConclusion), pb.BudgetConclusion_value, "BUDGET_CONCLUSION_"))}}
	if value.Destination != nil {
		out.Destination = coordinateProto(coordinateWire(*value.Destination))
	}
	for _, value := range value.Warnings {
		warning := &pb.Warning{Code: value.Code, Scope: pb.TargetScope(e.enum(string(value.Scope), pb.TargetScope_value, "TARGET_SCOPE_")), VisitId: rawID(value.VisitID), Message: value.Message}
		if value.LegPosition != nil {
			position := e.position(*value.LegPosition)
			warning.LegPosition = &position
		}
		out.Warnings = append(out.Warnings, warning)
	}
	for _, value := range value.Steps {
		step := &pb.RouteStep{VisitId: rawID(&value.VisitID), Kind: pb.VisitKind(e.enum(string(value.Kind), pb.VisitKind_value, "VISIT_KIND_")), Position: e.position(value.Position), ArrivalAt: e.instant(value.ArrivalAt), VisitStartAt: e.instant(value.VisitStartAt), VisitEndAt: e.instant(value.VisitEndAt), DepartureAt: e.instant(value.DepartureAt), MinDurationSeconds: value.MinDurationSeconds, Pinned: value.Pinned, Obligation: value.Obligation, Catalog: e.catalog(value.Catalog), Cost: e.cost(value.Cost), Participation: &pb.ParticipationSnapshot{Status: pb.ParticipationStatus(e.enum(string(value.Participation.Status), pb.ParticipationStatus_value, "PARTICIPATION_STATUS_")), Evidence: pb.ParticipationEvidence(e.enum(string(value.Participation.Evidence), pb.ParticipationEvidence_value, "PARTICIPATION_EVIDENCE_"))}}
		if value.Lunch != nil {
			step.Lunch = &pb.LunchMetadata{AfterVisitId: rawID(&value.Lunch.AfterVisitID), DurationSeconds: value.Lunch.DurationSeconds}
		}
		if value.ExternalVenue != nil {
			step.ExternalVenue = e.externalVenue(value.ExternalVenue)
		}
		for _, constraint := range value.AppliedConstraints {
			step.AppliedConstraints = append(step.AppliedConstraints, &pb.AppliedConstraint{Code: constraint.Code, Strength: pb.ConstraintStrength(e.enum(string(constraint.Strength), pb.ConstraintStrength_value, "CONSTRAINT_STRENGTH_")), Outcome: pb.ConstraintOutcome(e.enum(string(constraint.Outcome), pb.ConstraintOutcome_value, "CONSTRAINT_OUTCOME_")), Message: constraint.Message})
		}
		out.Steps = append(out.Steps, step)
	}
	for _, value := range value.Legs {
		out.Legs = append(out.Legs, &pb.RouteLeg{Position: e.position(value.Position), FromKind: pb.LegEndpointKind(e.enum(string(value.FromKind), pb.LegEndpointKind_value, "LEG_ENDPOINT_KIND_")), ToKind: pb.LegEndpointKind(e.enum(string(value.ToKind), pb.LegEndpointKind_value, "LEG_ENDPOINT_KIND_")), FromVisitId: rawID(value.FromVisitID), ToVisitId: rawID(value.ToVisitID), DepartureAt: e.instant(value.DepartureAt), ArrivalAt: e.instant(value.ArrivalAt), Mode: string(value.Mode), DistanceMeters: value.DistanceMeters, Geometry: planCoordinates(value.Geometry), Verification: pb.VerificationStatus(e.enum(string(value.Verification), pb.VerificationStatus_value, "VERIFICATION_STATUS_")), Evidence: &pb.LegEvidence{Provider: value.Evidence.Provider, Method: value.Evidence.Method, ObservedAt: e.instant(value.Evidence.ObservedAt), Mode: value.Evidence.Mode, Limitations: append([]string(nil), value.Evidence.Limitations...)}, Cost: e.cost(&value.Cost)})
	}
	return out
}
