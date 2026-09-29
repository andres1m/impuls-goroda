package routewire

import (
	"reflect"
	"strings"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
)

func BuildCancellationRecompute(routeID d.RouteID, city string, base d.RoutePlanSnapshot, history []d.Execution, visitIDs []d.VisitID, minCatalogRevision d.CatalogRevision) (*pb.RecomputeRequest, error) {
	request, err := buildRecomputeBase(routeID, city, base, history)
	if err != nil {
		return nil, err
	}
	if len(visitIDs) == 0 || minCatalogRevision.Validate() != nil {
		return nil, ErrInvalidRecomputeInput
	}
	affected := make(map[d.VisitID]bool, len(visitIDs))
	for _, id := range visitIDs {
		if id == (d.VisitID{}) || affected[id] {
			return nil, ErrInvalidRecomputeInput
		}
		affected[id] = true
	}
	trigger := &pb.CancellationTrigger{MinCatalogRevision: int64(minCatalogRevision)}
	for _, step := range base.Steps {
		if !affected[step.VisitID] {
			continue
		}
		if step.Kind != d.VisitPlace {
			return nil, ErrInvalidRecomputeInput
		}
		trigger.VisitIds = append(trigger.VisitIds, rawID(&step.VisitID))
	}
	if len(trigger.VisitIds) != len(visitIDs) {
		return nil, ErrInvalidRecomputeInput
	}
	request.Trigger = &pb.RecomputeRequest_Cancellation{Cancellation: trigger}
	return request, nil
}

func DecodeCheckedCancellationRecompute(routeID d.RouteID, city string, base d.RoutePlanSnapshot, history []d.Execution, visitIDs []d.VisitID, minCatalogRevision d.CatalogRevision, response *pb.RecomputeResponse) (RecomputedResult, error) {
	if _, err := BuildCancellationRecompute(routeID, city, base, history, visitIDs, minCatalogRevision); err != nil {
		return RecomputedResult{}, err
	}
	result, err := DecodeRecomputeResult(city, base, response)
	if err != nil {
		return RecomputedResult{}, err
	}
	if result.Diagnostics.CatalogRevision < int64(minCatalogRevision) {
		return RecomputedResult{}, ErrInvalidResult
	}
	executed := make(map[d.VisitID]d.Execution, len(history))
	for _, value := range history {
		executed[value.VisitID] = value
	}
	affected := make(map[d.VisitID]bool, len(visitIDs))
	for _, id := range visitIDs {
		if value, ok := executed[id]; !ok || value.Status == d.ExecutionPlanned {
			affected[id] = true
		}
	}
	switch result.Diagnostics.Status {
	case "PROPOSED":
		if result.Candidate == nil || validateCancellationCandidate(base, *result.Candidate, executed, affected, result.Changes) != nil {
			return RecomputedResult{}, ErrInvalidResult
		}
	case "UNCHANGED":
		if len(affected) != 0 {
			return RecomputedResult{}, ErrInvalidResult
		}
	case "CONFLICT":
		result.Candidate = nil
	}
	return result, nil
}

func validateCancellationCandidate(base, candidate d.RoutePlanSnapshot, executed map[d.VisitID]d.Execution, affected map[d.VisitID]bool, changes []RecomputedChange) error {
	if candidate.Validate() != nil || !connectedPlan(candidate) || base.SchemaVersion != candidate.SchemaVersion || base.Lifecycle != candidate.Lifecycle || base.ArchetypeID != candidate.ArchetypeID || base.Timezone != candidate.Timezone || !base.StartAt.Equal(candidate.StartAt) || !base.EndAt.Equal(candidate.EndAt) || base.Origin != candidate.Origin || !reflect.DeepEqual(base.Destination, candidate.Destination) || !reflect.DeepEqual(base.Constraints, candidate.Constraints) || candidate.CatalogRevision < base.CatalogRevision || len(candidate.Conflicts) != 0 || (candidate.Result != d.ResultReady && candidate.Result != d.ResultPartial) {
		return ErrInvalidResult
	}
	before := make(map[d.VisitID]d.RouteStep, len(base.Steps))
	after := make(map[d.VisitID]d.RouteStep, len(candidate.Steps))
	for _, step := range base.Steps {
		before[step.VisitID] = step
	}
	for i, step := range candidate.Steps {
		if step.Position != i+1 || affected[step.VisitID] {
			return ErrInvalidResult
		}
		for id := range affected {
			if sameVisitIdentity(before[id], step) {
				return ErrInvalidResult
			}
		}
		after[step.VisitID] = step
		if old, ok := before[step.VisitID]; ok {
			if !sameVisitIdentity(old, step) || step.Pinned != old.Pinned || step.Obligation != old.Obligation || step.Participation != old.Participation {
				return ErrInvalidResult
			}
		} else if step.Pinned || step.Obligation || step.Participation.Evidence != d.EvidenceNone || hasExternalCommitment(step) {
			return ErrInvalidResult
		}
	}
	for i, leg := range candidate.Legs {
		if leg.Position != i+1 {
			return ErrInvalidResult
		}
	}
	for _, value := range executed {
		old := before[value.VisitID]
		next, present := after[value.VisitID]
		switch value.Status {
		case d.ExecutionCompleted:
			if !present || !next.ArrivalAt.Equal(*value.ActualStartedAt) || !next.VisitStartAt.Equal(*value.ActualStartedAt) || !next.VisitEndAt.Equal(*value.ActualEndedAt) || !next.DepartureAt.Equal(*value.ActualEndedAt) || next.MinDurationSeconds > old.MinDurationSeconds || !reflect.DeepEqual(next.Catalog, old.Catalog) || !reflect.DeepEqual(next.Cost, old.Cost) {
				return ErrInvalidResult
			}
		case d.ExecutionSkipped:
			if present {
				return ErrInvalidResult
			}
		}
	}
	for _, old := range base.Steps {
		if value, ok := executed[old.VisitID]; ok && value.Status != d.ExecutionPlanned {
			continue
		}
		next, present := after[old.VisitID]
		if affected[old.VisitID] {
			if !explainsCancellation(changes, old.VisitID, hasExternalCommitment(old) || old.Obligation || hasConstraintObligation(base.Constraints, old)) {
				return ErrInvalidResult
			}
			continue
		}
		if (hasExternalCommitment(old) || old.Obligation || hasConstraintObligation(base.Constraints, old)) && (!present || !sameCommittedTime(old, next)) {
			return ErrInvalidResult
		}
		if !present && !explainsRemoval(changes, old.VisitID) {
			return ErrInvalidResult
		}
	}
	for _, obligation := range base.Constraints.Obligations {
		cancelled := false
		for id := range affected {
			if obligationMatches(obligation, before[id]) {
				cancelled = true
				break
			}
		}
		if cancelled {
			continue
		}
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

func ValidateCancellationCandidate(routeID d.RouteID, city string, base, candidate d.RoutePlanSnapshot, history []d.Execution, visitIDs []d.VisitID, minCatalogRevision d.CatalogRevision, changes []RecomputedChange) error {
	if _, err := BuildCancellationRecompute(routeID, city, base, history, visitIDs, minCatalogRevision); err != nil {
		return err
	}
	if candidate.CatalogRevision < minCatalogRevision {
		return ErrInvalidResult
	}
	executed := make(map[d.VisitID]d.Execution, len(history))
	for _, value := range history {
		executed[value.VisitID] = value
	}
	affected := make(map[d.VisitID]bool, len(visitIDs))
	for _, id := range visitIDs {
		if value, ok := executed[id]; !ok || value.Status == d.ExecutionPlanned {
			affected[id] = true
		}
	}
	return validateCancellationCandidate(base, candidate, executed, affected, changes)
}

func explainsCancellation(changes []RecomputedChange, id d.VisitID, commitment bool) bool {
	removed, action := false, false
	for _, change := range changes {
		if change.BeforeVisitID == nil || *change.BeforeVisitID != id || change.Scope != d.WarningVisit || strings.TrimSpace(change.Message) == "" {
			continue
		}
		if change.Kind == "removed" && change.AfterVisitID == nil {
			if removed {
				return false
			}
			removed = true
		}
		if change.Kind == "participation_action" && change.ParticipationAction != nil && strings.TrimSpace(*change.ParticipationAction) != "" {
			action = true
		}
	}
	return removed && (!commitment || action)
}
