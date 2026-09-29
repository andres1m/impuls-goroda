package routewire

import (
	"reflect"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
)

func DecodeCheckedRemovalRecompute(routeID d.RouteID, city string, base d.RoutePlanSnapshot, history []d.Execution, visitID d.VisitID, input RemovalProposalInput, response *pb.RecomputeResponse) (RecomputedResult, error) {
	if _, err := BuildRemovalRecompute(routeID, city, base, history, visitID, input); err != nil {
		return RecomputedResult{}, err
	}
	result, err := DecodeRecomputeResult(city, base, response)
	if err != nil {
		return RecomputedResult{}, err
	}
	switch result.Diagnostics.Status {
	case "PROPOSED":
		if result.Candidate == nil {
			return RecomputedResult{}, ErrInvalidResult
		}
		if err := ValidateRemovalCandidate(base, *result.Candidate, history, visitID, input, result.Changes); err != nil {
			return RecomputedResult{}, err
		}
	case "UNCHANGED":
		for _, execution := range history {
			if execution.VisitID == visitID && execution.Status != d.ExecutionPlanned {
				return result, nil
			}
		}
		return RecomputedResult{}, ErrInvalidResult
	case "CONFLICT":
		result.Candidate = nil
	}
	return result, nil
}

func ValidateRemovalCandidate(base, candidate d.RoutePlanSnapshot, history []d.Execution, visitID d.VisitID, input RemovalProposalInput, changes []RecomputedChange) error {
	if base.Validate() != nil || candidate.Validate() != nil || !connectedPlan(candidate) {
		return ErrInvalidResult
	}
	if _, err := input.protoMode(); err != nil {
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
	selected, exists := before[visitID]
	if !exists {
		return ErrInvalidRecomputeInput
	}
	if hasExternalCommitment(selected) && !input.AcknowledgeExternalCommitment {
		return ErrExternalCommitmentAcknowledgementRequired
	}
	for i, step := range candidate.Steps {
		if step.Position != i+1 || step.VisitID == visitID {
			return ErrInvalidResult
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
	executed := make(map[d.VisitID]d.Execution, len(history))
	for _, value := range history {
		if value.Validate() != nil {
			return ErrInvalidRecomputeInput
		}
		if _, duplicate := executed[value.VisitID]; duplicate {
			return ErrInvalidRecomputeInput
		}
		old, ok := before[value.VisitID]
		if !ok {
			return ErrInvalidRecomputeInput
		}
		executed[value.VisitID] = value
		next, present := after[value.VisitID]
		switch value.Status {
		case d.ExecutionCompleted:
			if value.ActualStartedAt == nil || value.ActualEndedAt == nil || !value.ActualEndedAt.After(*value.ActualStartedAt) {
				return ErrInvalidRecomputeInput
			}
			if !present || !next.ArrivalAt.Equal(*value.ActualStartedAt) || !next.VisitStartAt.Equal(*value.ActualStartedAt) || !next.VisitEndAt.Equal(*value.ActualEndedAt) || !next.DepartureAt.Equal(*value.ActualEndedAt) || next.MinDurationSeconds > old.MinDurationSeconds || !reflect.DeepEqual(next.Catalog, old.Catalog) || !reflect.DeepEqual(next.Cost, old.Cost) {
				return ErrInvalidResult
			}
		case d.ExecutionSkipped:
			if present || value.VisitID == visitID {
				return ErrInvalidResult
			}
		case d.ExecutionPlanned:
			if value.ActualStartedAt != nil || value.ActualEndedAt != nil {
				return ErrInvalidRecomputeInput
			}
		}
	}
	for _, old := range base.Steps {
		if execution, ok := executed[old.VisitID]; ok && execution.Status != d.ExecutionPlanned {
			continue
		}
		if old.VisitID == visitID {
			continue
		}
		next, present := after[old.VisitID]
		if (hasExternalCommitment(old) || old.Obligation || hasConstraintObligation(base.Constraints, old)) && (!present || !sameCommittedTime(old, next)) {
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
	return validateRemovalChange(selected, before, after, input, changes)
}

func validateRemovalChange(selected d.RouteStep, before, after map[d.VisitID]d.RouteStep, input RemovalProposalInput, changes []RecomputedChange) error {
	matched := false
	for _, change := range changes {
		if change.BeforeVisitID == nil || *change.BeforeVisitID != selected.VisitID || (change.Kind != "removed" && change.Kind != "replaced") {
			continue
		}
		if matched || change.Scope != d.WarningVisit || change.Message == "" {
			return ErrInvalidResult
		}
		matched = true
		if input.Mode == "rebuild" {
			if change.Kind != "removed" || change.AfterVisitID != nil {
				return ErrInvalidResult
			}
			continue
		}
		if change.Kind != "replaced" || change.AfterVisitID == nil {
			return ErrInvalidResult
		}
		pause, ok := after[*change.AfterVisitID]
		if _, old := before[*change.AfterVisitID]; old || !ok || pause.Kind != d.VisitFreeTime || pause.Catalog != nil || pause.Cost != nil || pause.Pinned || pause.Obligation || pause.Participation.Status != d.ParticipationNotRequired || pause.Participation.Evidence != d.EvidenceNone || !pause.ArrivalAt.Equal(selected.VisitStartAt) || !pause.VisitStartAt.Equal(selected.VisitStartAt) || !pause.VisitEndAt.Equal(selected.VisitEndAt) || !pause.DepartureAt.Equal(selected.VisitEndAt) {
			return ErrInvalidResult
		}
	}
	if !matched {
		return ErrInvalidResult
	}
	return nil
}
