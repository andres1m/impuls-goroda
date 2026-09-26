package grpchandler

import (
	"math"
	"time"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Results are validated before they are mapped, so these functions assume known enum
// values and consistent optional fields.

func optimizeResponseToProto(r domain.OptimizeResult) *pb.OptimizeResponse {
	out := &pb.OptimizeResponse{
		Status:            resultStatuses.toProto[r.Status],
		Warnings:          warningsToProto(r.Warnings),
		Conflicts:         conflictsToProto(r.Conflicts),
		Data:              freshnessToProto(r.Data),
		ComputationTimeMs: milliseconds(r.ComputationTime),
	}
	for _, route := range r.Routes {
		out.Routes = append(out.Routes, planToProto(route))
	}
	return out
}

func recomputeResponseToProto(r domain.RecomputeResult) *pb.RecomputeResponse {
	out := &pb.RecomputeResponse{
		Status:            recomputeStatuses.toProto[r.Status],
		Conflicts:         conflictsToProto(r.Conflicts),
		Data:              freshnessToProto(r.Data),
		ComputationTimeMs: milliseconds(r.ComputationTime),
	}
	if r.Candidate != nil {
		out.Candidate = planToProto(*r.Candidate)
	}
	for _, c := range r.Changes {
		out.Changes = append(out.Changes, changeToProto(c))
	}
	return out
}

func milliseconds(d time.Duration) uint32 {
	ms := d.Milliseconds()
	if ms < 0 {
		return 0
	}
	if ms > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(ms)
}

func freshnessToProto(f domain.DataFreshness) *pb.DataFreshness {
	return &pb.DataFreshness{
		DataMode:        dataModes.toProto[f.DataMode],
		DataAsOf:        optionalTimestamp(f.DataAsOf),
		CatalogRevision: int64(f.CatalogRevision),
	}
}

func changeToProto(c domain.RouteChange) *pb.RouteChange {
	out := &pb.RouteChange{
		Kind:          changeKinds.toProto[c.Kind],
		Scope:         scopes.toProto[c.Scope],
		BeforeVisitId: optionalIDBytes(c.BeforeVisitID),
		AfterVisitId:  optionalIDBytes(c.AfterVisitID),
		LegPosition:   optionalUint(c.LegPosition),
		Message:       c.Message,
	}
	switch d := c.Details.(type) {
	case domain.TimeShift:
		out.Details = &pb.RouteChange_TimeShiftSeconds{TimeShiftSeconds: int64(d.Delta / time.Second)}
	case domain.CostChange:
		out.Details = &pb.RouteChange_Cost{Cost: &pb.CostChange{Before: moneyToProto(d.Before), After: moneyToProto(d.After)}}
	case domain.ParticipationAction:
		out.Details = &pb.RouteChange_ParticipationAction{ParticipationAction: d.Action}
	case domain.VerificationChange:
		out.Details = &pb.RouteChange_Verification{Verification: &pb.VerificationChange{
			Before: verificationStatuses.toProto[d.Before],
			After:  verificationStatuses.toProto[d.After],
		}}
	}
	return out
}

func planToProto(p domain.Plan) *pb.RoutePlan {
	out := &pb.RoutePlan{
		Archetype:       archetypes.toProto[p.Archetype],
		StartAt:         timestamppb.New(p.Start),
		EndAt:           timestamppb.New(p.End),
		Origin:          coordinateToProto(p.Origin),
		CatalogRevision: int64(p.CatalogRevision),
		Result:          resultStatuses.toProto[p.Result],
		Warnings:        warningsToProto(p.Warnings),
		Conflicts:       conflictsToProto(p.Conflicts),
		Cost:            costSummaryToProto(p.Cost),
		Geometry:        coordinatesToProto(p.Geometry),
	}
	if p.Destination != nil {
		out.Destination = coordinateToProto(*p.Destination)
	}
	for _, s := range p.Steps {
		out.Steps = append(out.Steps, stepToProto(s))
	}
	for _, l := range p.Legs {
		out.Legs = append(out.Legs, legToProto(l))
	}
	return out
}

func idBytes[T ~[16]byte](id T) []byte {
	raw := make([]byte, len(id))
	copy(raw, id[:])
	return raw
}

func optionalIDBytes[T ~[16]byte](id *T) []byte {
	if id == nil {
		return nil
	}
	return idBytes(*id)
}

func idListBytes[T ~[16]byte](ids []T) [][]byte {
	if ids == nil {
		return nil
	}
	out := make([][]byte, len(ids))
	for i, id := range ids {
		out[i] = idBytes(id)
	}
	return out
}

func optionalTimestamp(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

func optionalUint(v *int) *uint32 {
	if v == nil {
		return nil
	}
	u := uint32(*v)
	return &u
}

func coordinateToProto(c domain.Coordinate) *pb.Coordinate {
	return &pb.Coordinate{Longitude: c.Longitude, Latitude: c.Latitude}
}

func coordinatesToProto(values []domain.Coordinate) []*pb.Coordinate {
	var out []*pb.Coordinate
	for _, c := range values {
		out = append(out, coordinateToProto(c))
	}
	return out
}

func moneyToProto(m domain.Money) *pb.Money {
	return &pb.Money{AmountMinor: m.AmountMinor, Currency: m.Currency}
}

func optionalMoneyToProto(m *domain.Money) *pb.Money {
	if m == nil {
		return nil
	}
	return moneyToProto(*m)
}

func warningsToProto(values []domain.Warning) []*pb.Warning {
	var out []*pb.Warning
	for _, w := range values {
		out = append(out, &pb.Warning{
			Code:        w.Code,
			Scope:       scopes.toProto[w.Scope],
			VisitId:     optionalIDBytes(w.VisitID),
			LegPosition: optionalUint(w.LegPosition),
			Message:     w.Message,
		})
	}
	return out
}

func conflictsToProto(values []domain.Conflict) []*pb.Conflict {
	var out []*pb.Conflict
	for _, c := range values {
		out = append(out, &pb.Conflict{
			Code:       c.Code,
			VisitIds:   idListBytes(c.VisitIDs),
			SessionIds: idListBytes(c.SessionIDs),
			Message:    c.Message,
		})
	}
	return out
}

func unknownComponentsToProto(values []domain.UnknownCostComponent) []*pb.UnknownCostComponent {
	var out []*pb.UnknownCostComponent
	for _, c := range values {
		out = append(out, &pb.UnknownCostComponent{Code: c.Code, Message: c.Message})
	}
	return out
}

func costSummaryToProto(c domain.CostSummary) *pb.CostSummary {
	return &pb.CostSummary{
		KnownPersonal:     moneyToProto(c.KnownPersonal),
		KnownTransport:    moneyToProto(c.KnownTransport),
		ProgramAmount:     moneyToProto(c.ProgramAmount),
		TotalLower:        optionalMoneyToProto(c.TotalLower),
		TotalUpper:        optionalMoneyToProto(c.TotalUpper),
		UnknownComponents: unknownComponentsToProto(c.UnknownComponents),
		BudgetConclusion:  budgetConclusions.toProto[c.BudgetConclusion],
	}
}

func provenanceToProto(p domain.Provenance) *pb.Provenance {
	return &pb.Provenance{
		SourceName:      p.SourceName,
		SourceUrl:       p.SourceURL,
		SourceRecordId:  optionalIDBytes(p.SourceRecordID),
		SourceUpdatedAt: optionalTimestamp(p.SourceUpdatedAt),
		FetchedAt:       timestamppb.New(p.FetchedAt),
		VerifiedAt:      optionalTimestamp(p.VerifiedAt),
	}
}

func costSnapshotToProto(c domain.CostSnapshot) *pb.CostSnapshot {
	return &pb.CostSnapshot{
		PriceOfferId: optionalIDBytes(c.PriceOfferID),
		Audience:     string(c.Audience),
		Price: &pb.Price{
			Status:     priceStatuses.toProto[c.Price.Status],
			Currency:   c.Price.Currency,
			LowerMinor: c.Price.LowerMinor,
			UpperMinor: c.Price.UpperMinor,
		},
		PersonalAmount:    optionalMoneyToProto(c.PersonalAmount),
		ProgramAmount:     optionalMoneyToProto(c.ProgramAmount),
		UnknownComponents: unknownComponentsToProto(c.UnknownComponents),
		Provenance:        provenanceToProto(c.Provenance),
	}
}

func catalogToProto(c domain.CatalogSnapshot) *pb.CatalogSnapshot {
	return &pb.CatalogSnapshot{
		PlaceId:             idBytes(c.PlaceID),
		EntranceId:          optionalIDBytes(c.EntranceID),
		EventId:             optionalIDBytes(c.EventID),
		SessionId:           optionalIDBytes(c.SessionID),
		Title:               c.Title,
		Category:            categories.toProto[c.Category],
		InterestMask:        uint64(c.InterestMask),
		Availability:        availabilities.toProto[c.Availability],
		RegistrationDetails: c.RegistrationDetails,
		AgeRequirements:     c.AgeRequirements,
		SessionStartsAt:     optionalTimestamp(c.SessionStart),
		SessionEndsAt:       optionalTimestamp(c.SessionEnd),
		SessionVersion:      c.SessionVersion,
		DataMode:            dataModes.toProto[c.DataMode],
		Provenance:          provenanceToProto(c.Provenance),
	}
}

func stepToProto(s domain.Step) *pb.RouteStep {
	out := &pb.RouteStep{
		VisitId:            idBytes(s.VisitID),
		Kind:               stepKinds.toProto[s.Kind],
		Position:           uint32(s.Position),
		ArrivalAt:          timestamppb.New(s.ArrivalAt),
		VisitStartAt:       timestamppb.New(s.VisitStartAt),
		VisitEndAt:         timestamppb.New(s.VisitEndAt),
		DepartureAt:        timestamppb.New(s.DepartureAt),
		MinDurationSeconds: int64(s.MinDuration / time.Second),
		Pinned:             s.Pinned,
		Obligation:         s.Obligation,
		Participation: &pb.ParticipationSnapshot{
			Status:   participationStatuses.toProto[s.Participation.Status],
			Evidence: participationEvidences.toProto[s.Participation.Evidence],
		},
	}
	if s.Catalog != nil {
		out.Catalog = catalogToProto(*s.Catalog)
	}
	if s.Cost != nil {
		out.Cost = costSnapshotToProto(*s.Cost)
	}
	for _, a := range s.AppliedConstraints {
		out.AppliedConstraints = append(out.AppliedConstraints, &pb.AppliedConstraint{
			Code:     a.Code,
			Strength: constraintStrengths.toProto[a.Strength],
			Outcome:  constraintOutcomes.toProto[a.Outcome],
			Message:  a.Message,
		})
	}
	return out
}

func legToProto(l domain.Leg) *pb.RouteLeg {
	return &pb.RouteLeg{
		Position:       uint32(l.Position),
		FromKind:       legEndpoints.toProto[l.From],
		ToKind:         legEndpoints.toProto[l.To],
		FromVisitId:    optionalIDBytes(l.FromVisitID),
		ToVisitId:      optionalIDBytes(l.ToVisitID),
		DepartureAt:    timestamppb.New(l.DepartureAt),
		ArrivalAt:      timestamppb.New(l.ArrivalAt),
		Mode:           string(l.Mode),
		DistanceMeters: l.DistanceMeters,
		Geometry:       coordinatesToProto(l.Geometry),
		Verification:   verificationStatuses.toProto[l.Verification],
		Evidence: &pb.LegEvidence{
			Provider:    l.Evidence.Provider,
			Method:      l.Evidence.Method,
			ObservedAt:  timestamppb.New(l.Evidence.ObservedAt),
			Mode:        l.Evidence.Mode,
			Limitations: l.Evidence.Limitations,
		},
		Cost: costSnapshotToProto(l.Cost),
	}
}
