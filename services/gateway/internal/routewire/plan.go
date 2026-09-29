package routewire

import (
	"slices"
	"strings"
	"time"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type planDecoder struct{ err error }

type ComputedResult struct {
	Diagnostics ResultDiagnostics
	Plans       []d.RoutePlanSnapshot
}

func DecodeResult(input ConfirmedRouteInput, response *pb.OptimizeResponse) (ComputedResult, error) {
	return decodeResult(input, response, connectedPlan)
}

func decodeResult(input ConfirmedRouteInput, response *pb.OptimizeResponse, connected func(d.RoutePlanSnapshot) bool) (ComputedResult, error) {
	diagnostics, err := DecodeDiagnostics(response)
	if err != nil {
		return ComputedResult{}, err
	}
	out := ComputedResult{Diagnostics: diagnostics, Plans: make([]d.RoutePlanSnapshot, 0, len(response.Routes))}
	seen := map[string]bool{}
	for _, candidate := range response.Routes {
		plan, err := planFromProto(input, candidate, connected)
		if err != nil {
			return ComputedResult{}, err
		}
		for _, step := range plan.Steps {
			if step.Catalog == nil {
				continue
			}
			if (diagnostics.DataMode == "live" && step.Catalog.DataMode != d.DataLive) || (diagnostics.DataMode == "prepared" && step.Catalog.DataMode == d.DataSynthetic) {
				return ComputedResult{}, ErrInvalidResult
			}
		}
		var signature []byte
		for _, step := range plan.Steps {
			if step.Catalog == nil {
				continue
			}
			signature = append(signature, step.Catalog.PlaceID[:]...)
			var session d.EventSessionID
			if step.Catalog.SessionID != nil {
				session = *step.Catalog.SessionID
			}
			signature = append(signature, session[:]...)
		}
		if seen[string(signature)] {
			return ComputedResult{}, ErrInvalidResult
		}
		seen[string(signature)] = true
		out.Plans = append(out.Plans, plan)
	}
	return out, nil
}

func PlanFromProto(input ConfirmedRouteInput, plan *pb.RoutePlan) (d.RoutePlanSnapshot, error) {
	return planFromProto(input, plan, connectedPlan)
}

func planFromProto(input ConfirmedRouteInput, plan *pb.RoutePlan, connected func(d.RoutePlanSnapshot) bool) (d.RoutePlanSnapshot, error) {
	if plan == nil || plan.Origin == nil || len(plan.Conflicts) != 0 {
		return d.RoutePlanSnapshot{}, ErrInvalidResult
	}
	constraints, err := input.DomainConstraints()
	if err != nil {
		return d.RoutePlanSnapshot{}, err
	}
	m := &planDecoder{}
	out := d.RoutePlanSnapshot{
		SchemaVersion: 1, Lifecycle: d.RouteDraft, Timezone: input.Timezone,
		ArchetypeID: m.enum(int32(plan.Archetype), pb.Archetype_name, "ARCHETYPE_"),
		StartAt:     m.instant(plan.StartAt), EndAt: m.instant(plan.EndAt),
		Origin: m.coordinate(plan.Origin), Constraints: constraints,
		CatalogRevision: d.CatalogRevision(plan.CatalogRevision),
		Result:          d.ResultStatus(strings.ToUpper(m.enum(int32(plan.Result), pb.ResultStatus_name, "RESULT_STATUS_"))),
		Cost:            m.summary(plan.Cost), Geometry: m.coordinates(plan.Geometry),
	}
	if plan.Destination != nil {
		value := m.coordinate(plan.Destination)
		out.Destination = &value
	}
	if !out.StartAt.Equal(input.StartAt) || !out.EndAt.Equal(input.EndAt) || out.Origin.Latitude != input.Origin.Latitude || out.Origin.Longitude != input.Origin.Longitude || (input.Destination == nil) != (out.Destination == nil) {
		return d.RoutePlanSnapshot{}, ErrInvalidResult
	}
	if input.Destination != nil && (input.Destination.Latitude != out.Destination.Latitude || input.Destination.Longitude != out.Destination.Longitude) {
		return d.RoutePlanSnapshot{}, ErrInvalidResult
	}
	if out.Result != d.ResultReady && out.Result != d.ResultPartial {
		return d.RoutePlanSnapshot{}, ErrInvalidResult
	}
	warnings, err := WarningsFromProto(plan.Warnings)
	if err != nil {
		return d.RoutePlanSnapshot{}, err
	}
	for _, warning := range warnings {
		value := d.Warning{Code: warning.Code, Message: warning.Message, Scope: d.WarningScope(warning.Scope)}
		if warning.VisitID != nil {
			bytes, err := optionalUUID(warning.VisitID)
			if err != nil {
				return d.RoutePlanSnapshot{}, ErrInvalidResult
			}
			value.VisitID = decodedID[d.VisitID](m, bytes, true)
		}
		if warning.LegPosition != nil {
			position := int(*warning.LegPosition)
			value.LegPosition = &position
		}
		out.Warnings = append(out.Warnings, value)
	}
	for i, step := range plan.Steps {
		if step == nil || step.Position != uint32(i+1) {
			return d.RoutePlanSnapshot{}, ErrInvalidResult
		}
		mapped := m.step(step)
		if mapped.Kind == d.VisitPlace && (mapped.Catalog == nil || mapped.Catalog.PlaceID == nil) {
			return d.RoutePlanSnapshot{}, ErrInvalidResult
		}
		if mapped.Cost != nil && mapped.Cost.PriceOfferID != nil && (mapped.Catalog == nil || mapped.Catalog.SessionID == nil) {
			return d.RoutePlanSnapshot{}, ErrInvalidResult
		}
		if mapped.ArrivalAt.Before(out.StartAt) || mapped.DepartureAt.After(out.EndAt) {
			return d.RoutePlanSnapshot{}, ErrInvalidResult
		}
		if i > 0 && mapped.ArrivalAt.Before(out.Steps[i-1].DepartureAt) {
			return d.RoutePlanSnapshot{}, ErrInvalidResult
		}
		out.Steps = append(out.Steps, mapped)
	}
	for i, leg := range plan.Legs {
		if leg == nil || leg.Position != uint32(i+1) {
			return d.RoutePlanSnapshot{}, ErrInvalidResult
		}
		mapped := m.leg(leg)
		if !slices.Contains(out.Constraints.MovementModes, mapped.Mode) {
			return d.RoutePlanSnapshot{}, ErrInvalidResult
		}
		if mapped.DepartureAt.Before(out.StartAt) || mapped.ArrivalAt.After(out.EndAt) {
			return d.RoutePlanSnapshot{}, ErrInvalidResult
		}
		out.Legs = append(out.Legs, mapped)
	}
	if m.err != nil || out.Validate() != nil || !connected(out) {
		return d.RoutePlanSnapshot{}, ErrInvalidResult
	}
	if out.Constraints.Budget.Mode == d.BudgetStrict && out.Cost.BudgetConclusion == d.BudgetViolated {
		return d.RoutePlanSnapshot{}, ErrInvalidResult
	}
	return out.Clone(), nil
}

func connectedPlan(plan d.RoutePlanSnapshot) bool {
	if plan.Resume != nil && (plan.Resume.LegPosition < 1 || plan.Resume.LegPosition > len(plan.Legs) || plan.Resume.DepartureAt.IsZero() || plan.Resume.Position.Validate() != nil) {
		return false
	}
	expected := len(plan.Steps)
	if plan.Destination != nil {
		expected++
	}
	if len(plan.Legs) != expected {
		return false
	}
	departure := plan.StartAt
	var previous *d.VisitID
	for i, step := range plan.Steps {
		leg := plan.Legs[i]
		if !resumeDeparture(plan, leg, &departure) {
			return false
		}
		if leg.ToKind != d.LegVisit || leg.ToVisitID == nil || *leg.ToVisitID != step.VisitID || !leg.ArrivalAt.Equal(step.ArrivalAt) || !leg.DepartureAt.Equal(departure) || !sameEndpoint(leg, previous) {
			return false
		}
		id := step.VisitID
		previous, departure = &id, step.DepartureAt
	}
	if plan.Destination != nil {
		leg := plan.Legs[len(plan.Legs)-1]
		if !resumeDeparture(plan, leg, &departure) {
			return false
		}
		if leg.ToKind != d.LegDestination || leg.ToVisitID != nil || !leg.DepartureAt.Equal(departure) || !sameEndpoint(leg, previous) {
			return false
		}
	}
	return true
}

func resumeDeparture(plan d.RoutePlanSnapshot, leg d.RouteLeg, departure *time.Time) bool {
	if plan.Resume == nil {
		return true
	}
	resume := plan.Resume
	if resume.LegPosition < 1 || resume.LegPosition > len(plan.Legs) || resume.DepartureAt.IsZero() || resume.Position.Validate() != nil {
		return false
	}
	if leg.Position != resume.LegPosition {
		return true
	}
	if resume.DepartureAt.Before(*departure) || len(leg.Geometry) == 0 || leg.Geometry[0] != resume.Position {
		return false
	}
	*departure = resume.DepartureAt
	return true
}

func sameEndpoint(leg d.RouteLeg, previous *d.VisitID) bool {
	if previous == nil {
		return leg.FromKind == d.LegOrigin && leg.FromVisitID == nil
	}
	return leg.FromKind == d.LegVisit && leg.FromVisitID != nil && *leg.FromVisitID == *previous
}

func (m *planDecoder) enum(value int32, names map[int32]string, prefix string) string {
	name, ok := names[value]
	if !ok || value == 0 {
		m.err = ErrInvalidResult
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(name, prefix))
}

func (m *planDecoder) instant(value *timestamppb.Timestamp) time.Time {
	instant, err := resultInstant(value, true)
	if err != nil {
		m.err = err
		return time.Time{}
	}
	return *instant
}

func (m *planDecoder) optionalInstant(value *timestamppb.Timestamp) *time.Time {
	instant, err := resultInstant(value, false)
	if err != nil {
		m.err = err
	}
	return instant
}

func (m *planDecoder) coordinate(value *pb.Coordinate) d.Coordinate {
	if value == nil {
		m.err = ErrInvalidResult
		return d.Coordinate{}
	}
	out := d.Coordinate{Latitude: value.Latitude, Longitude: value.Longitude}
	if out.Validate() != nil {
		m.err = ErrInvalidResult
	}
	return out
}

func (m *planDecoder) coordinates(values []*pb.Coordinate) []d.Coordinate {
	out := make([]d.Coordinate, 0, len(values))
	for _, value := range values {
		out = append(out, m.coordinate(value))
	}
	return out
}

func decodedID[T ~[16]byte](m *planDecoder, value []byte, required bool) *T {
	if len(value) == 0 && !required {
		return nil
	}
	if _, err := resultUUID(value); err != nil {
		m.err = err
		return nil
	}
	var id T
	copy(id[:], value)
	return &id
}

func (m *planDecoder) money(value *pb.Money) *d.Money {
	if value == nil {
		return nil
	}
	out := &d.Money{AmountMinor: value.AmountMinor, Currency: value.Currency}
	if out.Validate() != nil || len(out.Currency) != 3 || strings.Trim(out.Currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		m.err = ErrInvalidResult
	}
	return out
}

func (m *planDecoder) requiredMoney(value *pb.Money) d.Money {
	if value == nil {
		m.err = ErrInvalidResult
		return d.Money{}
	}
	return *m.money(value)
}

func (m *planDecoder) unknowns(values []*pb.UnknownCostComponent) []d.UnknownCostComponent {
	out := make([]d.UnknownCostComponent, 0, len(values))
	for _, value := range values {
		if value == nil || !validExplanation(value.Code, value.Message) {
			m.err = ErrInvalidResult
			continue
		}
		out = append(out, d.UnknownCostComponent{Code: value.Code, Message: value.Message})
	}
	return out
}

func (m *planDecoder) provenance(value *pb.Provenance) d.FactProvenance {
	if value == nil {
		m.err = ErrInvalidResult
		return d.FactProvenance{}
	}
	out := d.FactProvenance{SourceName: value.SourceName, SourceURL: value.SourceUrl,
		SourceRecordID:  decodedID[d.SourceRecordID](m, value.SourceRecordId, false),
		SourceUpdatedAt: m.optionalInstant(value.SourceUpdatedAt), FetchedAt: m.instant(value.FetchedAt), VerifiedAt: m.optionalInstant(value.VerifiedAt)}
	if out.Validate() != nil {
		m.err = ErrInvalidResult
	}
	return out
}

func (m *planDecoder) cost(value *pb.CostSnapshot) d.CostSnapshot {
	if value == nil || value.Price == nil {
		m.err = ErrInvalidResult
		return d.CostSnapshot{}
	}
	out := d.CostSnapshot{PriceOfferID: decodedID[d.PriceOfferID](m, value.PriceOfferId, false), Audience: value.Audience,
		Price:          d.Price{Status: d.PriceStatus(m.enum(int32(value.Price.Status), pb.PriceStatus_name, "PRICE_STATUS_")), Currency: value.Price.Currency, LowerMinor: value.Price.LowerMinor, UpperMinor: value.Price.UpperMinor},
		PersonalAmount: m.money(value.PersonalAmount), ProgramAmount: m.money(value.ProgramAmount),
		UnknownComponents: m.unknowns(value.UnknownComponents), Provenance: m.provenance(value.Provenance)}
	if out.Validate() != nil {
		m.err = ErrInvalidResult
	}
	return out
}

func (m *planDecoder) summary(value *pb.CostSummary) d.CostSummary {
	if value == nil {
		m.err = ErrInvalidResult
		return d.CostSummary{}
	}
	return d.CostSummary{KnownPersonal: m.requiredMoney(value.KnownPersonal), KnownTransport: m.requiredMoney(value.KnownTransport), ProgramAmount: m.requiredMoney(value.ProgramAmount), TotalLower: m.money(value.TotalLower), TotalUpper: m.money(value.TotalUpper), UnknownComponents: m.unknowns(value.UnknownComponents), BudgetConclusion: d.BudgetConclusion(m.enum(int32(value.BudgetConclusion), pb.BudgetConclusion_name, "BUDGET_CONCLUSION_"))}
}

func (m *planDecoder) catalog(value *pb.CatalogSnapshot) *d.CatalogSnapshot {
	if value == nil {
		return nil
	}
	out := &d.CatalogSnapshot{PlaceID: decodedID[d.PlaceID](m, value.PlaceId, false), EntranceID: decodedID[d.EntranceID](m, value.EntranceId, false), EventID: decodedID[d.EventID](m, value.EventId, false), SessionID: decodedID[d.EventSessionID](m, value.SessionId, false),
		Title: value.Title, InterestMask: value.InterestMask, Availability: d.AvailabilityStatus(m.enum(int32(value.Availability), pb.AvailabilityStatus_name, "AVAILABILITY_STATUS_")), RegistrationDetails: value.RegistrationDetails, AgeRequirements: value.AgeRequirements,
		SessionStartsAt: m.optionalInstant(value.SessionStartsAt), SessionEndsAt: m.optionalInstant(value.SessionEndsAt), SessionVersion: value.SessionVersion,
		DataMode: d.DataMode(m.enum(int32(value.DataMode), pb.DataMode_name, "DATA_MODE_")), Provenance: m.provenance(value.Provenance)}
	if value.Category != pb.Category_CATEGORY_UNSPECIFIED {
		out.Category = m.enum(int32(value.Category), pb.Category_name, "CATEGORY_")
	}
	return out
}

func (m *planDecoder) step(value *pb.RouteStep) d.RouteStep {
	out := d.RouteStep{Kind: d.VisitKind(m.enum(int32(value.Kind), pb.VisitKind_name, "VISIT_KIND_")), Position: int(value.Position),
		ArrivalAt: m.instant(value.ArrivalAt), VisitStartAt: m.instant(value.VisitStartAt), VisitEndAt: m.instant(value.VisitEndAt), DepartureAt: m.instant(value.DepartureAt),
		MinDurationSeconds: value.MinDurationSeconds, Pinned: value.Pinned, Obligation: value.Obligation, Catalog: m.catalog(value.Catalog)}
	if id := decodedID[d.VisitID](m, value.VisitId, true); id != nil {
		out.VisitID = *id
	}
	if value.MinDurationSeconds < 0 || value.MinDurationSeconds > 2147483647 || value.MinDurationSeconds > int64(out.VisitEndAt.Sub(out.VisitStartAt)/time.Second) {
		m.err = ErrInvalidResult
	}
	if value.Cost != nil {
		cost := m.cost(value.Cost)
		out.Cost = &cost
	}
	if value.Participation == nil {
		m.err = ErrInvalidResult
	} else {
		out.Participation = d.ParticipationSnapshot{Status: d.ParticipationStatus(m.enum(int32(value.Participation.Status), pb.ParticipationStatus_name, "PARTICIPATION_STATUS_")), Evidence: d.EvidenceSource(m.enum(int32(value.Participation.Evidence), pb.ParticipationEvidence_name, "PARTICIPATION_EVIDENCE_"))}
	}
	for _, constraint := range value.AppliedConstraints {
		if constraint == nil || !validExplanation(constraint.Code, constraint.Message) {
			m.err = ErrInvalidResult
			continue
		}
		out.AppliedConstraints = append(out.AppliedConstraints, d.AppliedConstraint{Code: constraint.Code, Message: constraint.Message, Strength: d.ConstraintStrength(m.enum(int32(constraint.Strength), pb.ConstraintStrength_name, "CONSTRAINT_STRENGTH_")), Outcome: d.ConstraintOutcome(m.enum(int32(constraint.Outcome), pb.ConstraintOutcome_name, "CONSTRAINT_OUTCOME_"))})
	}
	return out
}

func (m *planDecoder) leg(value *pb.RouteLeg) d.RouteLeg {
	out := d.RouteLeg{Position: int(value.Position), FromKind: d.LegEndpointKind(m.enum(int32(value.FromKind), pb.LegEndpointKind_name, "LEG_ENDPOINT_KIND_")), ToKind: d.LegEndpointKind(m.enum(int32(value.ToKind), pb.LegEndpointKind_name, "LEG_ENDPOINT_KIND_")),
		FromVisitID: decodedID[d.VisitID](m, value.FromVisitId, false), ToVisitID: decodedID[d.VisitID](m, value.ToVisitId, false),
		DepartureAt: m.instant(value.DepartureAt), ArrivalAt: m.instant(value.ArrivalAt), Mode: d.MovementMode(value.Mode), DistanceMeters: value.DistanceMeters, Geometry: m.coordinates(value.Geometry),
		Verification: d.VerificationStatus(m.enum(int32(value.Verification), pb.VerificationStatus_name, "VERIFICATION_STATUS_")), Cost: m.cost(value.Cost)}
	if value.Evidence == nil {
		m.err = ErrInvalidResult
	} else {
		out.Evidence = d.LegEvidence{Provider: value.Evidence.Provider, Method: value.Evidence.Method, ObservedAt: m.instant(value.Evidence.ObservedAt), Mode: value.Evidence.Mode, Limitations: append([]string(nil), value.Evidence.Limitations...)}
	}
	return out
}
