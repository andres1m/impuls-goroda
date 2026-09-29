package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type removalCandidate struct {
	FormatVersion int                            `json:"format_version"`
	City          string                         `json:"city"`
	VisitID       string                         `json:"visit_id"`
	Input         routewire.RemovalProposalInput `json:"input"`
	History       []d.Execution                  `json:"history"`
	Plan          d.RoutePlanSnapshot            `json:"plan"`
	Changes       []routewire.RecomputedChange   `json:"changes"`
}

func (q *Queries) CheckRecomputeBasis(ctx context.Context, state RecomputeState, catalog int64) (RouteAccess, error) {
	if _, ok := q.db.(pgx.Tx); !ok {
		return RouteAccess{}, errors.New("recompute basis check requires a transaction")
	}
	if err := q.LockCatalog(ctx, state.City, catalog); err != nil {
		return RouteAccess{}, err
	}
	access, err := q.LockOwnedRoute(ctx, state.Access.RouteID, state.Access.OwnerID)
	if err != nil {
		return RouteAccess{}, err
	}
	if err := RequireRevision(access, state.Access.Revision); err != nil {
		return RouteAccess{}, err
	}
	stored, err := q.ReadRecomputeState(ctx, access.RouteID, access.OwnerID)
	if err != nil {
		return RouteAccess{}, err
	}
	if access != state.Access || stored.City != state.City || !reflect.DeepEqual(stored.Plan, state.Plan) {
		return RouteAccess{}, routewire.ErrInvalidRecomputeInput
	}
	if err := q.checkPinHistory(ctx, access.RouteID, state.Plan, state.History); err != nil {
		return RouteAccess{}, err
	}
	if err := q.CheckCity(ctx, state.City, state.Plan.Timezone); err != nil {
		return RouteAccess{}, err
	}
	return access, nil
}

func (q *Queries) SaveRemovalProposal(ctx context.Context, state RecomputeState, visitID d.VisitID, input routewire.RemovalProposalInput, result routewire.RecomputedResult, now time.Time) (routewire.PendingProposal, error) {
	if result.Diagnostics.Status != "PROPOSED" || result.Candidate == nil || now.IsZero() {
		return routewire.PendingProposal{}, routewire.ErrInvalidResult
	}
	if _, err := routewire.BuildRemovalRecompute(state.Access.RouteID, state.City, state.Plan, state.History, visitID, input); err != nil {
		return routewire.PendingProposal{}, err
	}
	snapshot := result.Candidate.Clone()
	if int64(snapshot.CatalogRevision) != result.Diagnostics.CatalogRevision {
		return routewire.PendingProposal{}, routewire.ErrInvalidResult
	}
	if err := routewire.ValidateRemovalCandidate(state.Plan, snapshot, state.History, visitID, input, result.Changes); err != nil {
		return routewire.PendingProposal{}, err
	}
	access, err := q.CheckRecomputeBasis(ctx, state, result.Diagnostics.CatalogRevision)
	if err != nil {
		return routewire.PendingProposal{}, err
	}
	done := make(map[d.VisitID]bool, len(state.History))
	for _, execution := range state.History {
		done[execution.VisitID] = execution.Status == d.ExecutionCompleted
	}
	future := snapshot.Clone()
	future.Steps = nil
	for _, step := range snapshot.Steps {
		if !done[step.VisitID] {
			future.Steps = append(future.Steps, step)
		}
	}
	if err := q.CheckPlanCatalog(ctx, state.City, future); err != nil {
		return routewire.PendingProposal{}, err
	}
	changes, err := routewire.ProposalChangesToWire(result.Changes)
	if err != nil {
		return routewire.PendingProposal{}, err
	}
	newID, err := uuid.NewV7()
	if err != nil {
		return routewire.PendingProposal{}, err
	}
	proposal, err := routewire.PendingRemovalToWire(d.ProposalID(newID), access.Revision, snapshot.CatalogRevision, snapshot, changes, now)
	if err != nil {
		return routewire.PendingProposal{}, err
	}
	candidateJSON, err := json.Marshal(removalCandidate{FormatVersion: 1, City: state.City, VisitID: uuid.UUID(visitID).String(), Input: input, History: state.History, Plan: snapshot, Changes: result.Changes})
	if err != nil {
		return routewire.PendingProposal{}, err
	}
	changesJSON, err := json.Marshal(changes)
	if err != nil {
		return routewire.PendingProposal{}, err
	}
	route := encodeUUID([16]byte(access.RouteID))
	if _, err := q.db.Exec(ctx, `UPDATE planning.route_proposal SET state='invalidated',resolved_at=$2 WHERE route_id=$1 AND state='pending'`, route, now); err != nil {
		return routewire.PendingProposal{}, err
	}
	if _, err := q.db.Exec(ctx, `INSERT INTO planning.route_proposal
(id,route_id,base_revision,base_catalog_revision,reason,state,candidate_schema_version,candidate,changes,conflicts,created_at)
VALUES ($1,$2,$3,$4,'delete','pending',$5,$6,$7,'[]'::jsonb,$8)`, newID, route, int64(access.Revision), int64(snapshot.CatalogRevision), snapshot.SchemaVersion, candidateJSON, changesJSON, now); err != nil {
		return routewire.PendingProposal{}, err
	}
	return proposal, nil
}
