package routewire

import (
	"reflect"
	"time"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
)

func DecodeCheckedPanicRecompute(routeID d.RouteID, city string, base d.RoutePlanSnapshot, history []d.Execution, input PanicInput, now time.Time, response *pb.RecomputeResponse) (RecomputedResult, error) {
	request, err := BuildPanicRecompute(routeID, city, base, history, input, now)
	if err != nil {
		return RecomputedResult{}, err
	}
	effective := request.GetDelay().EffectiveStartAt.AsTime()
	connected := func(plan d.RoutePlanSnapshot) bool {
		return connectedPanicPlan(plan, history, effective, input.Position)
	}
	result, err := decodeRecomputeResult(city, base, response, connected)
	if err != nil {
		return RecomputedResult{}, err
	}
	if result.Diagnostics.Status == "CONFLICT" {
		result.Candidate = nil
		return result, nil
	}
	if result.Diagnostics.Status == "PROPOSED" {
		if result.Candidate == nil || ValidatePanicCandidate(base, *result.Candidate, history, input, now, result.Changes) != nil {
			return RecomputedResult{}, ErrInvalidResult
		}
	}
	return result, nil
}

func ValidatePanicCandidate(base, candidate d.RoutePlanSnapshot, history []d.Execution, input PanicInput, calculatedAt time.Time, changes []RecomputedChange) error {
	if err := ValidatePanicCandidateState(base, candidate, history, changes); err != nil {
		return err
	}
	return ValidatePanicPlanConnectivity(candidate, history, input, calculatedAt)
}

func ValidatePanicPlanConnectivity(candidate d.RoutePlanSnapshot, history []d.Execution, input PanicInput, calculatedAt time.Time) error {
	trigger, err := input.Proto(calculatedAt)
	if err != nil {
		return err
	}
	if candidate.Validate() != nil {
		return ErrInvalidResult
	}
	if !connectedPanicPlan(candidate, history, trigger.EffectiveStartAt.AsTime(), input.Position) {
		return ErrInvalidResult
	}
	return nil
}

func connectedPanicPlan(plan d.RoutePlanSnapshot, history []d.Execution, effective time.Time, position Coordinate) bool {
	expected := len(plan.Steps)
	if plan.Destination != nil {
		expected++
	}
	if len(plan.Legs) != expected {
		return false
	}
	completed := make(map[d.VisitID]d.Execution)
	seen := make(map[d.VisitID]bool, len(history))
	skipped := make(map[d.VisitID]bool)
	for _, execution := range history {
		if execution.Validate() != nil || seen[execution.VisitID] {
			return false
		}
		seen[execution.VisitID] = true
		if execution.Status == d.ExecutionCompleted {
			if execution.ActualStartedAt == nil || execution.ActualEndedAt == nil || !execution.ActualEndedAt.After(*execution.ActualStartedAt) {
				return false
			}
			completed[execution.VisitID] = execution
		} else if execution.Status == d.ExecutionSkipped {
			skipped[execution.VisitID] = true
		}
	}
	resume := len(completed)
	if resume > len(plan.Steps) {
		return false
	}
	departure := plan.StartAt
	var previous *d.VisitID
	for i, leg := range plan.Legs {
		if leg.Position != i+1 || !sameEndpoint(leg, previous) {
			return false
		}
		if i == resume {
			if effective.After(departure) {
				departure = effective
			}
			if len(leg.Geometry) == 0 || leg.Geometry[0].Latitude != position.Latitude || leg.Geometry[0].Longitude != position.Longitude {
				return false
			}
		}
		if !leg.DepartureAt.Equal(departure) {
			return false
		}
		if i < len(plan.Steps) {
			step := plan.Steps[i]
			execution, done := completed[step.VisitID]
			if done != (i < resume) || skipped[step.VisitID] || leg.ToKind != d.LegVisit || leg.ToVisitID == nil || *leg.ToVisitID != step.VisitID || !leg.ArrivalAt.Equal(step.ArrivalAt) {
				return false
			}
			if done && (!step.ArrivalAt.Equal(*execution.ActualStartedAt) || !step.VisitStartAt.Equal(*execution.ActualStartedAt) || !step.VisitEndAt.Equal(*execution.ActualEndedAt) || !step.DepartureAt.Equal(*execution.ActualEndedAt)) {
				return false
			}
			id := step.VisitID
			previous, departure = &id, step.DepartureAt
		} else if leg.ToKind != d.LegDestination || leg.ToVisitID != nil {
			return false
		}
	}
	return true
}

func ValidatePanicCandidateState(base, candidate d.RoutePlanSnapshot, history []d.Execution, changes []RecomputedChange) error {
	if base.Validate() != nil || candidate.Validate() != nil {
		return ErrInvalidResult
	}
	if base.SchemaVersion != candidate.SchemaVersion || base.Lifecycle != candidate.Lifecycle || base.ArchetypeID != candidate.ArchetypeID || base.Timezone != candidate.Timezone || !base.StartAt.Equal(candidate.StartAt) || !base.EndAt.Equal(candidate.EndAt) || base.Origin != candidate.Origin || !reflect.DeepEqual(base.Destination, candidate.Destination) || !reflect.DeepEqual(base.Constraints, candidate.Constraints) || candidate.CatalogRevision < base.CatalogRevision || len(candidate.Conflicts) != 0 || (candidate.Result != d.ResultReady && candidate.Result != d.ResultPartial) {
		return ErrInvalidResult
	}
	before := make(map[d.VisitID]d.RouteStep, len(base.Steps))
	after := make(map[d.VisitID]d.RouteStep, len(candidate.Steps))
	for _, step := range base.Steps {
		before[step.VisitID] = step
	}
	for i, step := range candidate.Steps {
		if step.Position != i+1 || step.ArrivalAt.Before(candidate.StartAt) || step.DepartureAt.After(candidate.EndAt) || (i > 0 && step.ArrivalAt.Before(candidate.Steps[i-1].DepartureAt)) {
			return ErrInvalidResult
		}
		after[step.VisitID] = step
		if old, ok := before[step.VisitID]; ok {
			if !sameVisitIdentity(old, step) || step.Pinned != old.Pinned || step.Obligation != old.Obligation || step.Participation != old.Participation {
				return ErrInvalidResult
			}
		} else if step.Pinned || step.Participation.Evidence != d.EvidenceNone || hasExternalCommitment(step) || (step.Obligation && !hasConstraintObligation(base.Constraints, step)) {
			return ErrInvalidResult
		}
	}
	executed := make(map[d.VisitID]d.Execution, len(history))
	for _, execution := range history {
		if execution.Validate() != nil {
			return ErrInvalidRecomputeInput
		}
		if _, duplicate := executed[execution.VisitID]; duplicate {
			return ErrInvalidRecomputeInput
		}
		if _, exists := before[execution.VisitID]; !exists {
			return ErrInvalidRecomputeInput
		}
		if execution.Status == d.ExecutionPlanned && (execution.ActualStartedAt != nil || execution.ActualEndedAt != nil) {
			return ErrInvalidRecomputeInput
		}
		executed[execution.VisitID] = execution
	}
	completed := 0
	for _, old := range base.Steps {
		execution, exists := executed[old.VisitID]
		next, present := after[old.VisitID]
		if exists && execution.Status == d.ExecutionCompleted {
			if execution.ActualStartedAt == nil || execution.ActualEndedAt == nil || !execution.ActualEndedAt.After(*execution.ActualStartedAt) {
				return ErrInvalidRecomputeInput
			}
			if !present || next.Position != completed+1 || !next.ArrivalAt.Equal(*execution.ActualStartedAt) || !next.VisitStartAt.Equal(*execution.ActualStartedAt) || !next.VisitEndAt.Equal(*execution.ActualEndedAt) || !next.DepartureAt.Equal(*execution.ActualEndedAt) || next.MinDurationSeconds > old.MinDurationSeconds || !reflect.DeepEqual(next.Catalog, old.Catalog) || !reflect.DeepEqual(next.Cost, old.Cost) {
				return ErrInvalidResult
			}
			completed++
			continue
		}
		if exists && execution.Status == d.ExecutionSkipped {
			if present {
				return ErrInvalidResult
			}
			continue
		}
		if (hasExternalCommitment(old) || old.Obligation || hasConstraintObligation(base.Constraints, old)) && (!present || !next.Obligation || !sameCommittedTime(old, next)) {
			return ErrInvalidResult
		}
		if !present && !explainsRemoval(changes, old.VisitID) {
			return ErrInvalidResult
		}
	}
	for _, obligation := range base.Constraints.Obligations {
		found := false
		for _, step := range candidate.Steps {
			matchesStart := obligation.StartsAt == nil || (step.Catalog != nil && step.Catalog.SessionStartsAt != nil && step.Catalog.SessionStartsAt.Equal(*obligation.StartsAt))
			if obligationMatches(obligation, step) && step.Obligation && matchesStart {
				found = true
				break
			}
		}
		if !found {
			return ErrInvalidResult
		}
	}
	return nil
}
