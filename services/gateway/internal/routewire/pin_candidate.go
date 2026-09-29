package routewire

import (
	"reflect"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
)

func DecodeCheckedPinRecompute(routeID d.RouteID, city string, base d.RoutePlanSnapshot, history []d.Execution, visitID d.VisitID, input PinVisitInput, response *pb.RecomputeResponse) (RecomputedResult, error) {
	if _, err := BuildPinRecompute(routeID, city, base, history, visitID, input); err != nil {
		return RecomputedResult{}, err
	}
	result, err := DecodePinRecompute(city, base, response)
	if err != nil {
		return RecomputedResult{}, err
	}
	if result.Diagnostics.Status == "PROPOSED" {
		if result.Candidate == nil {
			return RecomputedResult{}, ErrInvalidResult
		}
		if err := ValidatePinCandidate(base, *result.Candidate, history, visitID, input, result.Changes); err != nil {
			return RecomputedResult{}, err
		}
	} else if result.Diagnostics.Status == "UNCHANGED" {
		for _, execution := range history {
			if execution.VisitID == visitID && execution.Status != d.ExecutionPlanned {
				return result, nil
			}
		}
		for _, step := range base.Steps {
			if step.VisitID != visitID {
				continue
			}
			obligation := input.PinKind == "obligation" || hasExternalCommitment(step) || hasConstraintObligation(base.Constraints, step)
			if step.Pinned != (input.PinKind != "none") || step.Obligation != obligation {
				return RecomputedResult{}, ErrInvalidResult
			}
		}
	}
	return result, nil
}

func ValidatePinCandidate(base, candidate d.RoutePlanSnapshot, history []d.Execution, visitID d.VisitID, input PinVisitInput, changes []RecomputedChange) error {
	if base.Validate() != nil || candidate.Validate() != nil || !connectedPlan(candidate) {
		return ErrInvalidResult
	}
	if _, err := input.protoKind(); err != nil {
		return err
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
		if step.Position != i+1 {
			return ErrInvalidResult
		}
		after[step.VisitID] = step
		if old, ok := before[step.VisitID]; ok {
			if !sameVisitIdentity(old, step) {
				return ErrInvalidResult
			}
		} else if step.Pinned || step.Participation.Evidence != d.EvidenceNone || hasExternalCommitment(step) {
			return ErrInvalidResult
		}
	}
	selected, ok := before[visitID]
	if !ok || selected.Kind != d.VisitPlace {
		return ErrInvalidRecomputeInput
	}
	executed := make(map[d.VisitID]d.Execution, len(history))
	for _, value := range history {
		if value.Validate() != nil {
			return ErrInvalidRecomputeInput
		}
		if _, ok := executed[value.VisitID]; ok {
			return ErrInvalidRecomputeInput
		}
		old, exists := before[value.VisitID]
		if !exists {
			return ErrInvalidRecomputeInput
		}
		executed[value.VisitID] = value
		next, exists := after[value.VisitID]
		switch value.Status {
		case d.ExecutionCompleted:
			if value.ActualStartedAt == nil || value.ActualEndedAt == nil {
				return ErrInvalidRecomputeInput
			}
			if !exists || !next.ArrivalAt.Equal(*value.ActualStartedAt) || !next.VisitStartAt.Equal(*value.ActualStartedAt) || !next.VisitEndAt.Equal(*value.ActualEndedAt) || !next.DepartureAt.Equal(*value.ActualEndedAt) || next.MinDurationSeconds > old.MinDurationSeconds || next.Pinned != old.Pinned || next.Obligation != old.Obligation || !reflect.DeepEqual(next.Catalog, old.Catalog) || !reflect.DeepEqual(next.Cost, old.Cost) || next.Participation != old.Participation {
				return ErrInvalidResult
			}
		case d.ExecutionSkipped:
			if exists {
				return ErrInvalidResult
			}
		}
	}
	for _, old := range base.Steps {
		if execution, ok := executed[old.VisitID]; ok && execution.Status != d.ExecutionPlanned {
			continue
		}
		next, exists := after[old.VisitID]
		hard := hasExternalCommitment(old) || (old.Obligation && old.VisitID != visitID) || (old.VisitID == visitID && input.PinKind == "obligation") || hasConstraintObligation(base.Constraints, old)
		if hard && (!exists || !next.Obligation || !sameCommittedTime(old, next) || next.Participation != old.Participation) {
			return ErrInvalidResult
		}
		if old.Pinned && !exists && !explainsRemoval(changes, old.VisitID) {
			return ErrInvalidResult
		}
		if old.VisitID == visitID {
			if !exists {
				if input.PinKind == "obligation" || !explainsRemoval(changes, visitID) {
					return ErrInvalidResult
				}
				continue
			}
			if next.Pinned != (input.PinKind != "none") || next.Obligation != (hard || input.PinKind == "obligation") {
				return ErrInvalidResult
			}
		} else if exists && (next.Pinned != old.Pinned || next.Obligation != old.Obligation || next.Participation != old.Participation) {
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

func sameVisitIdentity(before, after d.RouteStep) bool {
	if before.Kind != after.Kind || (before.Catalog == nil) != (after.Catalog == nil) {
		return false
	}
	if before.Catalog == nil {
		return true
	}
	return reflect.DeepEqual(before.Catalog.PlaceID, after.Catalog.PlaceID) && reflect.DeepEqual(before.Catalog.EventID, after.Catalog.EventID) && reflect.DeepEqual(before.Catalog.SessionID, after.Catalog.SessionID)
}

func hasExternalCommitment(step d.RouteStep) bool {
	return step.Participation.Status == d.ParticipationUserReported || step.Participation.Status == d.ParticipationProviderConfirmed
}

func sameCommittedTime(before, after d.RouteStep) bool {
	return sameVisitIdentity(before, after) && before.VisitStartAt.Equal(after.VisitStartAt) && before.VisitEndAt.Equal(after.VisitEndAt)
}

func hasConstraintObligation(constraints d.RouteConstraints, step d.RouteStep) bool {
	for _, obligation := range constraints.Obligations {
		if obligationMatches(obligation, step) {
			return true
		}
	}
	return false
}

func obligationMatches(obligation d.RouteObligation, step d.RouteStep) bool {
	if obligation.VisitID != nil && *obligation.VisitID != step.VisitID {
		return false
	}
	return obligation.SessionID == nil || (step.Catalog != nil && step.Catalog.SessionID != nil && *obligation.SessionID == *step.Catalog.SessionID)
}

func explainsRemoval(changes []RecomputedChange, id d.VisitID) bool {
	for _, change := range changes {
		if change.BeforeVisitID != nil && *change.BeforeVisitID == id && (change.Kind == "removed" || change.Kind == "replaced") && change.Message != "" {
			return true
		}
	}
	return false
}
