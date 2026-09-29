package routewire

import (
	"strings"
	"unicode/utf8"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
)

type RecomputedResult struct {
	Diagnostics ResultDiagnostics
	Candidate   *d.RoutePlanSnapshot
	Changes     []RecomputedChange
}

type RecomputedChange struct {
	Kind                string
	Scope               d.WarningScope
	BeforeVisitID       *d.VisitID
	AfterVisitID        *d.VisitID
	LegPosition         *int
	Message             string
	TimeShiftSeconds    *int64
	CostBefore          *d.Money
	CostAfter           *d.Money
	ParticipationAction *string
	VerificationBefore  *d.VerificationStatus
	VerificationAfter   *d.VerificationStatus
}

func DecodePinRecompute(city string, base d.RoutePlanSnapshot, response *pb.RecomputeResponse) (RecomputedResult, error) {
	return DecodeRecomputeResult(city, base, response)
}

func DecodeRecomputeResult(city string, base d.RoutePlanSnapshot, response *pb.RecomputeResponse) (RecomputedResult, error) {
	return decodeRecomputeResult(city, base, response, connectedPlan)
}

func decodeRecomputeResult(city string, base d.RoutePlanSnapshot, response *pb.RecomputeResponse, connected func(d.RoutePlanSnapshot) bool) (RecomputedResult, error) {
	if base.Validate() != nil || response == nil || response.Data == nil || response.Data.CatalogRevision < int64(base.CatalogRevision) {
		return RecomputedResult{}, ErrInvalidResult
	}
	switch response.Status {
	case pb.RecomputeStatus_RECOMPUTE_STATUS_PROPOSED:
		if response.Candidate == nil || len(response.Conflicts) != 0 {
			return RecomputedResult{}, ErrInvalidResult
		}
	case pb.RecomputeStatus_RECOMPUTE_STATUS_UNCHANGED:
		if response.Candidate != nil || len(response.Changes) != 0 || len(response.Conflicts) != 0 {
			return RecomputedResult{}, ErrInvalidResult
		}
	case pb.RecomputeStatus_RECOMPUTE_STATUS_CONFLICT:
		if len(response.Conflicts) == 0 {
			return RecomputedResult{}, ErrInvalidResult
		}
	default:
		return RecomputedResult{}, ErrInvalidResult
	}
	m := planDecoder{}
	out := RecomputedResult{Diagnostics: ResultDiagnostics{Status: strings.TrimPrefix(response.Status.String(), "RECOMPUTE_STATUS_"), CatalogRevision: response.Data.CatalogRevision, ComputationTimeMS: int64(response.ComputationTimeMs), DataMode: m.enum(int32(response.Data.DataMode), pb.DataMode_name, "DATA_MODE_")}}
	var err error
	out.Diagnostics.DataAsOf, err = resultInstant(response.Data.DataAsOf, false)
	if err != nil || m.err != nil || response.Data.CatalogRevision <= 0 {
		return RecomputedResult{}, ErrInvalidResult
	}
	out.Diagnostics.Conflicts, err = ConflictsFromProto(response.Conflicts)
	if err != nil {
		return RecomputedResult{}, err
	}
	if response.Candidate != nil {
		base = base.Clone()
		input := ConfirmedRouteInput{City: city, Timezone: base.Timezone, StartAt: base.StartAt, EndAt: base.EndAt, Origin: coordinateWire(base.Origin), Constraints: constraintsWire(base.Constraints)}
		if base.Destination != nil {
			point := coordinateWire(*base.Destination)
			input.Destination = &point
		}
		candidate := response.Candidate
		decoded, err := decodeResult(input, &pb.OptimizeResponse{Status: candidate.Result, Routes: []*pb.RoutePlan{candidate}, Data: response.Data, ComputationTimeMs: response.ComputationTimeMs}, connected)
		if err != nil || len(decoded.Plans) != 1 || decoded.Plans[0].ArchetypeID != base.ArchetypeID {
			return RecomputedResult{}, ErrInvalidResult
		}
		plan := decoded.Plans[0]
		plan.SchemaVersion, plan.Lifecycle, plan.Constraints = base.SchemaVersion, base.Lifecycle, base.Constraints
		out.Diagnostics.Warnings, err = WarningsFromProto(candidate.Warnings)
		if err != nil {
			return RecomputedResult{}, err
		}
		copy := plan.Clone()
		out.Candidate = &copy
	}
	out.Changes, err = decodeRecomputedChanges(response.Changes, base, out.Candidate)
	if err != nil {
		return RecomputedResult{}, err
	}
	return out, nil
}

func decodeRecomputedChanges(values []*pb.RouteChange, base d.RoutePlanSnapshot, candidate *d.RoutePlanSnapshot) ([]RecomputedChange, error) {
	m := planDecoder{}
	before := make(map[d.VisitID]bool, len(base.Steps))
	after := make(map[d.VisitID]bool)
	for _, step := range base.Steps {
		before[step.VisitID] = true
	}
	if candidate != nil {
		for _, step := range candidate.Steps {
			after[step.VisitID] = true
		}
	}
	out := make([]RecomputedChange, 0, len(values))
	for _, value := range values {
		if value == nil || strings.TrimSpace(value.Message) == "" || !utf8.ValidString(value.Message) || utf8.RuneCountInString(value.Message) > 512 {
			return nil, ErrInvalidResult
		}
		change := RecomputedChange{Kind: m.enum(int32(value.Kind), pb.RouteChangeKind_name, "ROUTE_CHANGE_KIND_"), Scope: d.WarningScope(m.enum(int32(value.Scope), pb.TargetScope_name, "TARGET_SCOPE_")), BeforeVisitID: decodedID[d.VisitID](&m, value.BeforeVisitId, false), AfterVisitID: decodedID[d.VisitID](&m, value.AfterVisitId, false), Message: value.Message}
		if m.err != nil || (change.BeforeVisitID != nil && !before[*change.BeforeVisitID]) || (candidate != nil && change.AfterVisitID != nil && !after[*change.AfterVisitID]) {
			return nil, ErrInvalidResult
		}
		if value.LegPosition != nil {
			if *value.LegPosition == 0 || uint64(*value.LegPosition) > 2147483647 {
				return nil, ErrInvalidResult
			}
			position := int(*value.LegPosition)
			change.LegPosition = &position
		}
		hasVisit := change.BeforeVisitID != nil || change.AfterVisitID != nil
		switch change.Scope {
		case d.WarningRoute:
			if hasVisit || change.LegPosition != nil {
				return nil, ErrInvalidResult
			}
		case d.WarningVisit:
			if !hasVisit || change.LegPosition != nil {
				return nil, ErrInvalidResult
			}
		case d.WarningLeg:
			if hasVisit || change.LegPosition == nil {
				return nil, ErrInvalidResult
			}
			limit := len(base.Legs)
			if candidate != nil && len(candidate.Legs) > limit {
				limit = len(candidate.Legs)
			}
			if *change.LegPosition > limit {
				return nil, ErrInvalidResult
			}
		default:
			return nil, ErrInvalidResult
		}
		switch value.Kind {
		case pb.RouteChangeKind_ROUTE_CHANGE_KIND_KEPT, pb.RouteChangeKind_ROUTE_CHANGE_KIND_REMOVED, pb.RouteChangeKind_ROUTE_CHANGE_KIND_REPLACED:
			if change.Scope != d.WarningVisit || value.Details != nil || (value.Kind == pb.RouteChangeKind_ROUTE_CHANGE_KIND_KEPT && change.BeforeVisitID == nil) || (value.Kind == pb.RouteChangeKind_ROUTE_CHANGE_KIND_REMOVED && change.AfterVisitID != nil) || (value.Kind == pb.RouteChangeKind_ROUTE_CHANGE_KIND_REPLACED && (change.BeforeVisitID == nil || change.AfterVisitID == nil)) {
				return nil, ErrInvalidResult
			}
		case pb.RouteChangeKind_ROUTE_CHANGE_KIND_TIME_SHIFTED:
			details, ok := value.Details.(*pb.RouteChange_TimeShiftSeconds)
			if !ok || details == nil || change.Scope != d.WarningVisit {
				return nil, ErrInvalidResult
			}
			seconds := details.TimeShiftSeconds
			change.TimeShiftSeconds = &seconds
		case pb.RouteChangeKind_ROUTE_CHANGE_KIND_COST_CHANGED:
			details, ok := value.Details.(*pb.RouteChange_Cost)
			if !ok || details == nil || details.Cost == nil || details.Cost.Before == nil || details.Cost.After == nil {
				return nil, ErrInvalidResult
			}
			change.CostBefore, change.CostAfter = m.money(details.Cost.Before), m.money(details.Cost.After)
			if change.CostBefore.Currency != change.CostAfter.Currency {
				return nil, ErrInvalidResult
			}
		case pb.RouteChangeKind_ROUTE_CHANGE_KIND_PARTICIPATION_ACTION:
			details, ok := value.Details.(*pb.RouteChange_ParticipationAction)
			if !ok || details == nil || change.Scope != d.WarningVisit || strings.TrimSpace(details.ParticipationAction) == "" || !utf8.ValidString(details.ParticipationAction) {
				return nil, ErrInvalidResult
			}
			action := details.ParticipationAction
			change.ParticipationAction = &action
		case pb.RouteChangeKind_ROUTE_CHANGE_KIND_VERIFICATION_CHANGED:
			details, ok := value.Details.(*pb.RouteChange_Verification)
			if !ok || details == nil || details.Verification == nil || change.Scope != d.WarningLeg {
				return nil, ErrInvalidResult
			}
			previous := d.VerificationStatus(m.enum(int32(details.Verification.Before), pb.VerificationStatus_name, "VERIFICATION_STATUS_"))
			next := d.VerificationStatus(m.enum(int32(details.Verification.After), pb.VerificationStatus_name, "VERIFICATION_STATUS_"))
			change.VerificationBefore, change.VerificationAfter = &previous, &next
		default:
			return nil, ErrInvalidResult
		}
		out = append(out, change)
	}
	if m.err != nil {
		return nil, m.err
	}
	return out, nil
}
