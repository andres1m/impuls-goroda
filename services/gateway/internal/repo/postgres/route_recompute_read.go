package postgres

import (
	"context"
	"encoding/json"
	"strconv"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/google/uuid"
)

type RecomputeState struct {
	Access  RouteAccess
	City    string
	Plan    d.RoutePlanSnapshot
	History []d.Execution
}

func (q *Queries) ReadRecomputeState(ctx context.Context, id d.RouteID, actor d.UserID) (RecomputeState, error) {
	if id == (d.RouteID{}) || actor == (d.UserID{}) {
		return RecomputeState{}, routewire.ErrInvalidRecomputeInput
	}
	var raw []byte
	if err := q.db.QueryRow(ctx, ownerRouteSQL, encodeUUID([16]byte(id)), encodeUUID([16]byte(actor))).Scan(&raw); err != nil {
		return RecomputeState{}, mapQueryError("read recompute state", err)
	}
	var stored struct {
		RouteID   string                     `json:"route_id"`
		Revision  string                     `json:"revision"`
		City      string                     `json:"city"`
		Lifecycle d.RouteLifecycle           `json:"lifecycle"`
		Plan      json.RawMessage            `json:"plan"`
		Execution []routewire.OwnerExecution `json:"execution"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		return RecomputeState{}, err
	}
	revision, err := strconv.ParseInt(stored.Revision, 10, 64)
	if err != nil || revision <= 0 || strconv.FormatInt(revision, 10) != stored.Revision || stored.RouteID != uuid.UUID(id).String() || stored.City == "" {
		return RecomputeState{}, routewire.ErrInvalidResult
	}
	plan, err := routewire.DecodeStoredPlan(stored.Plan)
	if err != nil {
		resume, proofErr := q.readAppliedResume(ctx, id, actor, d.RouteRevisionNumber(revision))
		if proofErr != nil {
			return RecomputeState{}, proofErr
		}
		plan, err = routewire.DecodeStoredPlanWithResume(stored.Plan, resume)
		if err != nil {
			return RecomputeState{}, err
		}
	}
	if plan.Lifecycle != stored.Lifecycle {
		return RecomputeState{}, routewire.ErrInvalidResult
	}
	out := RecomputeState{Access: RouteAccess{RouteID: id, OwnerID: actor, Lifecycle: stored.Lifecycle, Revision: d.RouteRevisionNumber(revision)}, City: stored.City, Plan: plan}
	current := make(map[d.VisitID]bool, len(plan.Steps))
	for _, step := range plan.Steps {
		current[step.VisitID] = true
	}
	seen := make(map[d.VisitID]bool)
	for _, value := range stored.Execution {
		visit, err := uuid.Parse(value.VisitID)
		if err != nil || visit == uuid.Nil {
			return RecomputeState{}, routewire.ErrInvalidResult
		}
		visitID := d.VisitID(visit)
		if seen[visitID] {
			return RecomputeState{}, routewire.ErrInvalidResult
		}
		seen[visitID] = true
		updated, err := strconv.ParseInt(value.UpdatedInRevision, 10, 64)
		if err != nil || updated > revision || strconv.FormatInt(updated, 10) != value.UpdatedInRevision {
			return RecomputeState{}, routewire.ErrInvalidResult
		}
		execution := d.Execution{RouteID: id, VisitID: visitID, Status: d.ExecutionStatus(value.Status), Confirmation: d.ConfirmationKind(value.ConfirmationKind), ActualStartedAt: value.ActualStartedAt, ActualEndedAt: value.ActualEndedAt, UpdatedInRevision: d.RouteRevisionNumber(updated), UpdatedAt: value.UpdatedAt}
		if err := execution.Validate(); err != nil {
			return RecomputeState{}, err
		}
		if current[visitID] {
			out.History = append(out.History, execution)
		}
	}
	for id := range current {
		if !seen[id] {
			return RecomputeState{}, routewire.ErrInvalidResult
		}
	}
	return out, nil
}
