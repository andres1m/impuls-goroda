package grpchandler

import (
	"fmt"
	"math"
	"time"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// fieldError points at the wire field that could not be read, in proto field notation.
type fieldError struct {
	Field  string
	Reason string
}

func (e *fieldError) Error() string {
	return e.Field + ": " + e.Reason
}

const (
	reasonRequired    = "is required"
	reasonUnsupported = "has an unsupported value"
)

// reader keeps the first field error so that mapping code stays linear.
type reader struct {
	err *fieldError
}

func (r *reader) fail(field, reason string) {
	if r.err == nil {
		r.err = &fieldError{Field: field, Reason: reason}
	}
}

func (r *reader) result() error {
	if r.err == nil {
		return nil
	}
	return r.err
}

func join(prefix, name string) string {
	return prefix + "." + name
}

func index(prefix string, i int) string {
	return fmt.Sprintf("%s[%d]", prefix, i)
}

func requiredID[T ~[16]byte](r *reader, field string, raw []byte) T {
	if len(raw) == 0 {
		r.fail(field, reasonRequired)
		return T{}
	}
	return optionalIDValue[T](r, field, raw)
}

func optionalID[T ~[16]byte](r *reader, field string, raw []byte) *T {
	if len(raw) == 0 {
		return nil
	}
	id := optionalIDValue[T](r, field, raw)
	return &id
}

func optionalIDValue[T ~[16]byte](r *reader, field string, raw []byte) T {
	var id T
	if len(raw) != len(id) {
		r.fail(field, "must be 16 bytes")
		return id
	}
	copy(id[:], raw)
	return id
}

func idList[T ~[16]byte](r *reader, field string, raw [][]byte) []T {
	if raw == nil {
		return nil
	}
	ids := make([]T, len(raw))
	for i, v := range raw {
		ids[i] = requiredID[T](r, index(field, i), v)
	}
	return ids
}

func enumValue[P comparable, D comparable](r *reader, field string, m enumMap[P, D], v P) D {
	d, ok := m.toDomain[v]
	if !ok {
		r.fail(field, reasonUnsupported)
	}
	return d
}

func enumList[P comparable, D comparable](r *reader, field string, m enumMap[P, D], values []P) []D {
	if values == nil {
		return nil
	}
	out := make([]D, len(values))
	for i, v := range values {
		out[i] = enumValue(r, index(field, i), m, v)
	}
	return out
}

func requiredTime(r *reader, field string, ts *timestamppb.Timestamp) time.Time {
	if ts == nil {
		r.fail(field, reasonRequired)
		return time.Time{}
	}
	return timeValue(r, field, ts)
}

func optionalTime(r *reader, field string, ts *timestamppb.Timestamp) *time.Time {
	if ts == nil {
		return nil
	}
	t := timeValue(r, field, ts)
	return &t
}

func timeValue(r *reader, field string, ts *timestamppb.Timestamp) time.Time {
	if err := ts.CheckValid(); err != nil {
		r.fail(field, "is not a valid timestamp")
		return time.Time{}
	}
	return ts.AsTime()
}

func seconds(r *reader, field string, s int64) time.Duration {
	if s > math.MaxInt64/int64(time.Second) || s < math.MinInt64/int64(time.Second) {
		r.fail(field, "is out of range")
		return 0
	}
	return time.Duration(s) * time.Second
}

func optionalInt(v *uint32) *int {
	if v == nil {
		return nil
	}
	i := int(*v)
	return &i
}

func movementModes(values []string) []domain.MovementMode {
	if values == nil {
		return nil
	}
	out := make([]domain.MovementMode, len(values))
	for i, v := range values {
		out[i] = domain.MovementMode(v)
	}
	return out
}

func requiredCoordinate(r *reader, field string, c *pb.Coordinate) domain.Coordinate {
	if c == nil {
		r.fail(field, reasonRequired)
		return domain.Coordinate{}
	}
	return domain.Coordinate{Longitude: c.GetLongitude(), Latitude: c.GetLatitude()}
}

func optionalCoordinate(c *pb.Coordinate) *domain.Coordinate {
	if c == nil {
		return nil
	}
	return &domain.Coordinate{Longitude: c.GetLongitude(), Latitude: c.GetLatitude()}
}

func coordinates(values []*pb.Coordinate) []domain.Coordinate {
	if values == nil {
		return nil
	}
	out := make([]domain.Coordinate, len(values))
	for i, c := range values {
		out[i] = domain.Coordinate{Longitude: c.GetLongitude(), Latitude: c.GetLatitude()}
	}
	return out
}

func requiredMoney(r *reader, field string, m *pb.Money) domain.Money {
	if m == nil {
		r.fail(field, reasonRequired)
		return domain.Money{}
	}
	return domain.Money{AmountMinor: m.GetAmountMinor(), Currency: m.GetCurrency()}
}

func optionalMoney(m *pb.Money) *domain.Money {
	if m == nil {
		return nil
	}
	return &domain.Money{AmountMinor: m.GetAmountMinor(), Currency: m.GetCurrency()}
}

func optimizeRequestFromProto(in *pb.OptimizeRequest) (domain.OptimizeRequest, error) {
	r := &reader{}
	out := domain.OptimizeRequest{
		City:        in.GetCity(),
		Timezone:    in.GetTimezone(),
		Start:       requiredTime(r, "start_at", in.GetStartAt()),
		End:         requiredTime(r, "end_at", in.GetEndAt()),
		Origin:      requiredCoordinate(r, "origin", in.GetOrigin()),
		Destination: optionalCoordinate(in.GetDestination()),
		Constraints: constraintsFromProto(r, "constraints", in.GetConstraints()),
	}
	return out, r.result()
}

func recomputeRequestFromProto(in *pb.RecomputeRequest) (domain.RecomputeRequest, error) {
	r := &reader{}
	out := domain.RecomputeRequest{
		City:        in.GetCity(),
		Timezone:    in.GetTimezone(),
		Constraints: constraintsFromProto(r, "constraints", in.GetConstraints()),
		Trigger:     triggerFromProto(r, in),
	}
	if in.GetBasePlan() == nil {
		r.fail("base_plan", reasonRequired)
	} else {
		out.Base = planFromProto(r, "base_plan", in.GetBasePlan())
	}
	for i, e := range in.GetHistory() {
		field := index("history", i)
		out.History = append(out.History, domain.VisitExecution{
			VisitID:     requiredID[domain.VisitID](r, join(field, "visit_id"), e.GetVisitId()),
			Status:      enumValue(r, join(field, "status"), executionStatuses, e.GetStatus()),
			ActualStart: optionalTime(r, join(field, "actual_started_at"), e.GetActualStartedAt()),
			ActualEnd:   optionalTime(r, join(field, "actual_ended_at"), e.GetActualEndedAt()),
		})
	}
	return out, r.result()
}

func triggerFromProto(r *reader, in *pb.RecomputeRequest) domain.Trigger {
	switch t := in.GetTrigger().(type) {
	case *pb.RecomputeRequest_Delay:
		d := t.Delay
		return domain.DelayTrigger{
			Mode:           enumValue(r, "delay.mode", delayModes, d.GetMode()),
			EffectiveStart: requiredTime(r, "delay.effective_start_at", d.GetEffectiveStartAt()),
			Position:       requiredCoordinate(r, "delay.position", d.GetPosition()),
			PositionSource: enumValue(r, "delay.position_source", positionSources, d.GetPositionSource()),
		}
	case *pb.RecomputeRequest_Cancellation:
		c := t.Cancellation
		return domain.CancellationTrigger{
			VisitIDs:           idList[domain.VisitID](r, "cancellation.visit_ids", c.GetVisitIds()),
			MinCatalogRevision: domain.CatalogRevision(c.GetMinCatalogRevision()),
		}
	case *pb.RecomputeRequest_Removal:
		rm := t.Removal
		return domain.RemovalTrigger{
			VisitID: requiredID[domain.VisitID](r, "removal.visit_id", rm.GetVisitId()),
			Mode:    enumValue(r, "removal.mode", removalModes, rm.GetMode()),
		}
	case *pb.RecomputeRequest_Pin:
		p := t.Pin
		return domain.PinTrigger{
			VisitID: requiredID[domain.VisitID](r, "pin.visit_id", p.GetVisitId()),
			Kind:    enumValue(r, "pin.kind", pinKinds, p.GetKind()),
		}
	default:
		r.fail("trigger", reasonRequired)
		return nil
	}
}

func constraintsFromProto(r *reader, field string, c *pb.RouteConstraints) domain.RouteConstraints {
	if c == nil {
		r.fail(field, reasonRequired)
		return domain.RouteConstraints{}
	}
	out := domain.RouteConstraints{
		InterestMask:       domain.InterestMask(c.GetInterestMask()),
		ExcludedCategories: enumList(r, join(field, "excluded_categories"), categories, c.GetExcludedCategories()),
		MovementModes:      movementModes(c.GetMovementModes()),
		LoadProfile:        c.GetLoadProfile(),
		BenefitPrograms:    c.GetBenefitPrograms(),
		AudienceClaims:     c.GetAudienceClaims(),
		SoftPreferences:    c.GetSoftPreferences(),
		AcceptedUnknowns:   c.GetAcceptedUnknowns(),
	}
	if b := c.GetBudget(); b == nil {
		r.fail(join(field, "budget"), reasonRequired)
	} else {
		out.Budget = domain.Budget{
			Mode:  enumValue(r, join(field, "budget.mode"), budgetModes, b.GetMode()),
			Limit: optionalMoney(b.GetLimit()),
		}
	}
	if pbal := c.GetProgramBalance(); pbal != nil {
		out.ProgramBalance = &domain.ProgramBalance{
			Program: pbal.GetProgram(),
			Balance: requiredMoney(r, join(field, "program_balance.balance"), pbal.GetBalance()),
		}
	}
	for i, o := range c.GetObligations() {
		f := index(join(field, "obligations"), i)
		out.Obligations = append(out.Obligations, domain.Obligation{
			VisitID:       optionalID[domain.VisitID](r, join(f, "visit_id"), o.GetVisitId()),
			SessionID:     optionalID[domain.SessionID](r, join(f, "session_id"), o.GetSessionId()),
			StartsAt:      optionalTime(r, join(f, "starts_at"), o.GetStartsAt()),
			ArrivalBuffer: seconds(r, join(f, "arrival_buffer_seconds"), o.GetArrivalBufferSeconds()),
			Participation: enumValue(r, join(f, "participation"), participationStatuses, o.GetParticipation()),
		})
	}
	if l := c.GetLunchWindow(); l != nil {
		f := join(field, "lunch_window")
		out.LunchWindow = &domain.LunchWindow{
			Start:       requiredTime(r, join(f, "start_at"), l.GetStartAt()),
			End:         requiredTime(r, join(f, "end_at"), l.GetEndAt()),
			MinDuration: seconds(r, join(f, "min_duration_seconds"), l.GetMinDurationSeconds()),
		}
	}
	return out
}

func planFromProto(r *reader, field string, p *pb.RoutePlan) domain.Plan {
	out := domain.Plan{
		Archetype:       enumValue(r, join(field, "archetype"), archetypes, p.GetArchetype()),
		Start:           requiredTime(r, join(field, "start_at"), p.GetStartAt()),
		End:             requiredTime(r, join(field, "end_at"), p.GetEndAt()),
		Origin:          requiredCoordinate(r, join(field, "origin"), p.GetOrigin()),
		Destination:     optionalCoordinate(p.GetDestination()),
		CatalogRevision: domain.CatalogRevision(p.GetCatalogRevision()),
		Result:          enumValue(r, join(field, "result"), resultStatuses, p.GetResult()),
		Warnings:        warningsFromProto(r, join(field, "warnings"), p.GetWarnings()),
		Conflicts:       conflictsFromProto(r, join(field, "conflicts"), p.GetConflicts()),
		Cost:            costSummaryFromProto(r, join(field, "cost"), p.GetCost()),
		Geometry:        coordinates(p.GetGeometry()),
	}
	for i, s := range p.GetSteps() {
		out.Steps = append(out.Steps, stepFromProto(r, index(join(field, "steps"), i), s))
	}
	for i, l := range p.GetLegs() {
		out.Legs = append(out.Legs, legFromProto(r, index(join(field, "legs"), i), l))
	}
	return out
}

func warningsFromProto(r *reader, field string, values []*pb.Warning) []domain.Warning {
	var out []domain.Warning
	for i, w := range values {
		f := index(field, i)
		out = append(out, domain.Warning{
			Code:        w.GetCode(),
			Scope:       enumValue(r, join(f, "scope"), scopes, w.GetScope()),
			VisitID:     optionalID[domain.VisitID](r, join(f, "visit_id"), w.GetVisitId()),
			LegPosition: optionalInt(w.LegPosition),
			Message:     w.GetMessage(),
		})
	}
	return out
}

func conflictsFromProto(r *reader, field string, values []*pb.Conflict) []domain.Conflict {
	var out []domain.Conflict
	for i, c := range values {
		f := index(field, i)
		out = append(out, domain.Conflict{
			Code:       c.GetCode(),
			VisitIDs:   idList[domain.VisitID](r, join(f, "visit_ids"), c.GetVisitIds()),
			SessionIDs: idList[domain.SessionID](r, join(f, "session_ids"), c.GetSessionIds()),
			Message:    c.GetMessage(),
		})
	}
	return out
}

func unknownComponentsFromProto(values []*pb.UnknownCostComponent) []domain.UnknownCostComponent {
	var out []domain.UnknownCostComponent
	for _, c := range values {
		out = append(out, domain.UnknownCostComponent{Code: c.GetCode(), Message: c.GetMessage()})
	}
	return out
}

func costSummaryFromProto(r *reader, field string, c *pb.CostSummary) domain.CostSummary {
	if c == nil {
		r.fail(field, reasonRequired)
		return domain.CostSummary{}
	}
	return domain.CostSummary{
		KnownPersonal:     requiredMoney(r, join(field, "known_personal"), c.GetKnownPersonal()),
		KnownTransport:    requiredMoney(r, join(field, "known_transport"), c.GetKnownTransport()),
		ProgramAmount:     requiredMoney(r, join(field, "program_amount"), c.GetProgramAmount()),
		TotalLower:        optionalMoney(c.GetTotalLower()),
		TotalUpper:        optionalMoney(c.GetTotalUpper()),
		UnknownComponents: unknownComponentsFromProto(c.GetUnknownComponents()),
		BudgetConclusion:  enumValue(r, join(field, "budget_conclusion"), budgetConclusions, c.GetBudgetConclusion()),
	}
}

func provenanceFromProto(r *reader, field string, p *pb.Provenance) domain.Provenance {
	if p == nil {
		r.fail(field, reasonRequired)
		return domain.Provenance{}
	}
	return domain.Provenance{
		SourceName:      p.GetSourceName(),
		SourceURL:       p.SourceUrl,
		SourceRecordID:  optionalID[domain.SourceRecordID](r, join(field, "source_record_id"), p.GetSourceRecordId()),
		SourceUpdatedAt: optionalTime(r, join(field, "source_updated_at"), p.GetSourceUpdatedAt()),
		FetchedAt:       requiredTime(r, join(field, "fetched_at"), p.GetFetchedAt()),
		VerifiedAt:      optionalTime(r, join(field, "verified_at"), p.GetVerifiedAt()),
	}
}

func priceFromProto(r *reader, field string, p *pb.Price) domain.Price {
	if p == nil {
		r.fail(field, reasonRequired)
		return domain.Price{}
	}
	return domain.Price{
		Status:     enumValue(r, join(field, "status"), priceStatuses, p.GetStatus()),
		Currency:   p.GetCurrency(),
		LowerMinor: p.LowerMinor,
		UpperMinor: p.UpperMinor,
	}
}

func costSnapshotFromProto(r *reader, field string, c *pb.CostSnapshot) domain.CostSnapshot {
	return domain.CostSnapshot{
		PriceOfferID:      optionalID[domain.PriceOfferID](r, join(field, "price_offer_id"), c.GetPriceOfferId()),
		Audience:          domain.Audience(c.GetAudience()),
		Price:             priceFromProto(r, join(field, "price"), c.GetPrice()),
		PersonalAmount:    optionalMoney(c.GetPersonalAmount()),
		ProgramAmount:     optionalMoney(c.GetProgramAmount()),
		UnknownComponents: unknownComponentsFromProto(c.GetUnknownComponents()),
		Provenance:        provenanceFromProto(r, join(field, "provenance"), c.GetProvenance()),
	}
}

func catalogFromProto(r *reader, field string, c *pb.CatalogSnapshot) *domain.CatalogSnapshot {
	if c == nil {
		return nil
	}
	return &domain.CatalogSnapshot{
		PlaceID:             requiredID[domain.PlaceID](r, join(field, "place_id"), c.GetPlaceId()),
		EntranceID:          optionalID[domain.EntranceID](r, join(field, "entrance_id"), c.GetEntranceId()),
		EventID:             optionalID[domain.EventID](r, join(field, "event_id"), c.GetEventId()),
		SessionID:           optionalID[domain.SessionID](r, join(field, "session_id"), c.GetSessionId()),
		Title:               c.GetTitle(),
		Category:            enumValue(r, join(field, "category"), categories, c.GetCategory()),
		InterestMask:        domain.InterestMask(c.GetInterestMask()),
		Availability:        enumValue(r, join(field, "availability"), availabilities, c.GetAvailability()),
		RegistrationDetails: c.GetRegistrationDetails(),
		AgeRequirements:     c.GetAgeRequirements(),
		SessionStart:        optionalTime(r, join(field, "session_starts_at"), c.GetSessionStartsAt()),
		SessionEnd:          optionalTime(r, join(field, "session_ends_at"), c.GetSessionEndsAt()),
		SessionVersion:      c.GetSessionVersion(),
		DataMode:            enumValue(r, join(field, "data_mode"), dataModes, c.GetDataMode()),
		Provenance:          provenanceFromProto(r, join(field, "provenance"), c.GetProvenance()),
	}
}

func stepFromProto(r *reader, field string, s *pb.RouteStep) domain.Step {
	out := domain.Step{
		VisitID:      requiredID[domain.VisitID](r, join(field, "visit_id"), s.GetVisitId()),
		Kind:         enumValue(r, join(field, "kind"), stepKinds, s.GetKind()),
		Position:     int(s.GetPosition()),
		ArrivalAt:    requiredTime(r, join(field, "arrival_at"), s.GetArrivalAt()),
		VisitStartAt: requiredTime(r, join(field, "visit_start_at"), s.GetVisitStartAt()),
		VisitEndAt:   requiredTime(r, join(field, "visit_end_at"), s.GetVisitEndAt()),
		DepartureAt:  requiredTime(r, join(field, "departure_at"), s.GetDepartureAt()),
		MinDuration:  seconds(r, join(field, "min_duration_seconds"), s.GetMinDurationSeconds()),
		Pinned:       s.GetPinned(),
		Obligation:   s.GetObligation(),
		Catalog:      catalogFromProto(r, join(field, "catalog"), s.GetCatalog()),
	}
	if p := s.GetParticipation(); p == nil {
		r.fail(join(field, "participation"), reasonRequired)
	} else {
		out.Participation = domain.Participation{
			Status:   enumValue(r, join(field, "participation.status"), participationStatuses, p.GetStatus()),
			Evidence: enumValue(r, join(field, "participation.evidence"), participationEvidences, p.GetEvidence()),
		}
	}
	if c := s.GetCost(); c != nil {
		cost := costSnapshotFromProto(r, join(field, "cost"), c)
		out.Cost = &cost
	}
	for i, a := range s.GetAppliedConstraints() {
		f := index(join(field, "applied_constraints"), i)
		out.AppliedConstraints = append(out.AppliedConstraints, domain.AppliedConstraint{
			Code:     a.GetCode(),
			Strength: enumValue(r, join(f, "strength"), constraintStrengths, a.GetStrength()),
			Outcome:  enumValue(r, join(f, "outcome"), constraintOutcomes, a.GetOutcome()),
			Message:  a.GetMessage(),
		})
	}
	return out
}

func legFromProto(r *reader, field string, l *pb.RouteLeg) domain.Leg {
	out := domain.Leg{
		Position:       int(l.GetPosition()),
		From:           enumValue(r, join(field, "from_kind"), legEndpoints, l.GetFromKind()),
		To:             enumValue(r, join(field, "to_kind"), legEndpoints, l.GetToKind()),
		FromVisitID:    optionalID[domain.VisitID](r, join(field, "from_visit_id"), l.GetFromVisitId()),
		ToVisitID:      optionalID[domain.VisitID](r, join(field, "to_visit_id"), l.GetToVisitId()),
		DepartureAt:    requiredTime(r, join(field, "departure_at"), l.GetDepartureAt()),
		ArrivalAt:      requiredTime(r, join(field, "arrival_at"), l.GetArrivalAt()),
		Mode:           domain.MovementMode(l.GetMode()),
		DistanceMeters: l.DistanceMeters,
		Geometry:       coordinates(l.GetGeometry()),
		Verification:   enumValue(r, join(field, "verification"), verificationStatuses, l.GetVerification()),
	}
	if e := l.GetEvidence(); e == nil {
		r.fail(join(field, "evidence"), reasonRequired)
	} else {
		out.Evidence = domain.LegEvidence{
			Provider:    e.GetProvider(),
			Method:      e.GetMethod(),
			ObservedAt:  requiredTime(r, join(field, "evidence.observed_at"), e.GetObservedAt()),
			Mode:        e.GetMode(),
			Limitations: e.GetLimitations(),
		}
	}
	if c := l.GetCost(); c == nil {
		r.fail(join(field, "cost"), reasonRequired)
	} else {
		out.Cost = costSnapshotFromProto(r, join(field, "cost"), c)
	}
	return out
}
