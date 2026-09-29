package usecase

import (
	"context"
	"slices"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// CopyRoute preserves the shared route's ordered visits while recomputing their timing,
// catalog facts, prices and travel from the recipient's origin.
func (p *Planner) CopyRoute(ctx context.Context, input *domain.CopyRequest) (domain.CopyResult, error) {
	if err := input.Validate(); err != nil {
		return domain.CopyResult{}, err
	}
	base := input.Base
	base.Steps = slices.Clone(input.Base.Steps)
	base.Legs = slices.Clone(input.Base.Legs)
	base.Origin = input.Origin
	base.Destination = input.Destination
	base.Warnings = nil // diagnostics belong to the author's computation
	base.Conflicts = nil
	ids := make(map[domain.VisitID]domain.VisitID, len(base.Steps))
	var expected []*domain.CatalogSnapshot
	for i := range base.Steps {
		step := &base.Steps[i]
		fresh := p.newID()
		ids[step.VisitID] = fresh
		step.VisitID = fresh
		step.Pinned = false
		status := step.Participation.Status
		if status != domain.ParticipationNotRequired {
			status = domain.ParticipationActionRequired
		}
		step.Participation = domain.Participation{Status: status, Evidence: domain.EvidenceNone}
		step.AppliedConstraints = nil
		if step.Kind == domain.StepVisit {
			step.Obligation = true // enforce the source's exact visit sequence during repair
			expected = append(expected, step.Catalog)
		}
	}
	for i := range base.Legs {
		leg := &base.Legs[i]
		if leg.FromVisitID != nil {
			id := ids[*leg.FromVisitID]
			leg.FromVisitID = &id
		}
		if leg.ToVisitID != nil {
			id := ids[*leg.ToVisitID]
			leg.ToVisitID = &id
		}
	}
	constraints := input.Constraints
	constraints.Obligations = slices.Clone(input.Constraints.Obligations)
	for i := range constraints.Obligations {
		if constraints.Obligations[i].Participation != domain.ParticipationNotRequired {
			constraints.Obligations[i].Participation = domain.ParticipationActionRequired
		}
		if id := constraints.Obligations[i].VisitID; id != nil {
			fresh, ok := ids[*id]
			if !ok {
				return domain.CopyResult{}, ErrInvalidRequest
			}
			constraints.Obligations[i].VisitID = &fresh
		}
	}
	recipientCommitments := make(map[domain.VisitID]bool, len(constraints.Obligations))
	for i := range base.Steps {
		step := &base.Steps[i]
		if step.Kind == domain.StepVisit && slices.ContainsFunc(constraints.Obligations, func(o domain.Obligation) bool {
			return obligationMatches(o, step)
		}) {
			recipientCommitments[step.VisitID] = true
		}
	}
	request := domain.RecomputeRequest{City: input.City, Timezone: input.Timezone, Base: base,
		Constraints: constraints, Trigger: domain.CopyTrigger{Origin: input.Origin}}
	if err := request.Validate(); err != nil {
		return domain.CopyResult{}, err
	}
	res, err := p.Recompute(ctx, &request)
	if err != nil {
		return domain.CopyResult{}, err
	}
	result := domain.CopyResult{Data: res.Data, ComputationTime: res.ComputationTime, Conflicts: res.Conflicts}
	if res.Candidate == nil {
		result.Status = domain.ResultConflict
		if len(result.Conflicts) == 0 {
			result.Conflicts = []domain.Conflict{{Code: "NO_FEASIBLE_ROUTE", Message: "The shared visits no longer fit in the day"}}
		}
		if len(result.Conflicts) == 1 && result.Conflicts[0].Code == "NO_FEASIBLE_ROUTE" {
			result.Status = domain.ResultNoFeasibleRoute
		}
		return result, nil
	}
	var actual []*domain.CatalogSnapshot
	for _, step := range res.Candidate.Steps {
		if step.Kind == domain.StepVisit {
			actual = append(actual, step.Catalog)
		}
	}
	if len(actual) != len(expected) || !slices.EqualFunc(actual, expected, sameCopyVisit) {
		result.Status = domain.ResultNoFeasibleRoute
		result.Conflicts = []domain.Conflict{{Code: "NO_FEASIBLE_ROUTE", Message: "The shared visit order cannot be preserved"}}
		return result, nil
	}
	for i := range res.Candidate.Steps {
		step := &res.Candidate.Steps[i]
		if step.Kind != domain.StepVisit || recipientCommitments[step.VisitID] {
			continue
		}
		step.Obligation = false
		step.AppliedConstraints = slices.DeleteFunc(step.AppliedConstraints, func(c domain.AppliedConstraint) bool {
			return c.Code == obligationReachable
		})
	}
	res.Candidate.Warnings = slices.DeleteFunc(res.Candidate.Warnings, func(w domain.Warning) bool {
		return w.Code == "UNVERIFIED_TRANSITION" && w.VisitID != nil && !recipientCommitments[*w.VisitID]
	})
	result.Route = res.Candidate
	result.Status = result.Route.Result
	result.Warnings = result.Route.Warnings
	return result, nil
}

func sameCopyVisit(a, b *domain.CatalogSnapshot) bool {
	if a.PlaceID != b.PlaceID || (a.EventID == nil) != (b.EventID == nil) ||
		(a.SessionID == nil) != (b.SessionID == nil) {
		return false
	}
	if a.EventID != nil && *a.EventID != *b.EventID {
		return false
	}
	return a.SessionID == nil || *a.SessionID == *b.SessionID
}
