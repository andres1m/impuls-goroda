package postgres

import (
	"context"
	"encoding/json"
	"time"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/google/uuid"
)

type panicCandidate struct {
	FormatVersion int                          `json:"format_version"`
	City          string                       `json:"city"`
	Input         routewire.PanicInput         `json:"input"`
	CalculatedAt  time.Time                    `json:"calculated_at"`
	History       []d.Execution                `json:"history"`
	Plan          d.RoutePlanSnapshot          `json:"plan"`
	Changes       []routewire.RecomputedChange `json:"changes"`
}

func (q *Queries) SavePanicProposal(ctx context.Context, state RecomputeState, input routewire.PanicInput, calculatedAt time.Time, result routewire.RecomputedResult, now time.Time) (routewire.PendingProposal, error) {
	if result.Diagnostics.Status != "PROPOSED" || result.Candidate == nil || now.IsZero() || calculatedAt.IsZero() || now.Before(calculatedAt) {
		return routewire.PendingProposal{}, routewire.ErrInvalidResult
	}
	request, err := routewire.BuildPanicRecompute(state.Access.RouteID, state.City, state.Plan, state.History, input, calculatedAt)
	if err != nil {
		return routewire.PendingProposal{}, err
	}
	snapshot := result.Candidate.Clone()
	if int64(snapshot.CatalogRevision) != result.Diagnostics.CatalogRevision {
		return routewire.PendingProposal{}, routewire.ErrInvalidResult
	}
	if err := routewire.ValidatePanicCandidate(state.Plan, snapshot, state.History, input, calculatedAt, result.Changes); err != nil {
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
	effective := request.GetDelay().EffectiveStartAt.AsTime()
	proposal, err := routewire.PendingPanicToWire(d.ProposalID(newID), access.Revision, snapshot.CatalogRevision, snapshot, changes, now, effective)
	if err != nil {
		return routewire.PendingProposal{}, err
	}
	candidateJSON, err := json.Marshal(panicCandidate{FormatVersion: 1, City: state.City, Input: input, CalculatedAt: calculatedAt, History: state.History, Plan: snapshot, Changes: result.Changes})
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
(id,route_id,base_revision,base_catalog_revision,reason,state,candidate_schema_version,candidate,changes,conflicts,effective_start_at,created_at)
VALUES ($1,$2,$3,$4,'delay','pending',$5,$6,$7,'[]'::jsonb,$8,$9)`, newID, route, int64(access.Revision), int64(snapshot.CatalogRevision), snapshot.SchemaVersion, candidateJSON, changesJSON, effective, now); err != nil {
		return routewire.PendingProposal{}, err
	}
	return proposal, nil
}
