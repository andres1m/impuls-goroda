package postgres

import (
	"context"
	"encoding/json"
	"reflect"
	"time"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type lunchCandidate struct {
	FormatVersion int                          `json:"format_version"`
	City          string                       `json:"city"`
	Intent        routewire.LunchIntent        `json:"intent"`
	History       []d.Execution                `json:"history"`
	Plan          d.RoutePlanSnapshot          `json:"plan"`
	Changes       []routewire.RecomputedChange `json:"changes"`
}

type LunchProposalRecord struct {
	ID              d.ProposalID
	BaseRevision    d.RouteRevisionNumber
	CatalogRevision d.CatalogRevision
	State           d.ProposalState
	CreatedAt       time.Time
	Candidate       lunchCandidate
}

func (q *Queries) SaveLunchProposal(ctx context.Context, state RecomputeState, intent routewire.LunchIntent, result routewire.RecomputedResult, now time.Time) (routewire.PendingProposal, error) {
	if result.Diagnostics.Status != "PROPOSED" || result.Candidate == nil || now.IsZero() {
		return routewire.PendingProposal{}, routewire.ErrInvalidResult
	}
	if _, err := routewire.BuildLunchRecompute(state.Access.RouteID, state.City, state.Plan, state.History, intent); err != nil {
		return routewire.PendingProposal{}, err
	}
	snapshot := result.Candidate.Clone()
	if int64(snapshot.CatalogRevision) != result.Diagnostics.CatalogRevision || routewire.ValidateLunchCandidate(state.Plan, snapshot, state.History, intent, result.Changes) != nil {
		return routewire.PendingProposal{}, routewire.ErrInvalidResult
	}
	access, err := q.CheckRecomputeBasis(ctx, state, result.Diagnostics.CatalogRevision)
	if err != nil {
		return routewire.PendingProposal{}, err
	}
	future := snapshot.Clone()
	future.Steps = nil
	done := map[d.VisitID]bool{}
	for _, execution := range state.History {
		done[execution.VisitID] = execution.Status == d.ExecutionCompleted
	}
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
	id, err := uuid.NewV7()
	if err != nil {
		return routewire.PendingProposal{}, err
	}
	proposal, err := routewire.PendingLunchToWire(d.ProposalID(id), access.Revision, snapshot.CatalogRevision, snapshot, changes, now)
	if err != nil {
		return routewire.PendingProposal{}, err
	}
	candidate, err := json.Marshal(lunchCandidate{FormatVersion: 1, City: state.City, Intent: intent, History: state.History, Plan: snapshot, Changes: result.Changes})
	if err != nil {
		return routewire.PendingProposal{}, err
	}
	changeJSON, err := json.Marshal(changes)
	if err != nil {
		return routewire.PendingProposal{}, err
	}
	route := encodeUUID([16]byte(access.RouteID))
	if _, err := q.db.Exec(ctx, `UPDATE planning.route_proposal SET state='invalidated',resolved_at=$2 WHERE route_id=$1 AND state='pending'`, route, now); err != nil {
		return routewire.PendingProposal{}, err
	}
	if _, err := q.db.Exec(ctx, `INSERT INTO planning.route_proposal (id,route_id,base_revision,base_catalog_revision,reason,state,candidate_schema_version,candidate,changes,conflicts,created_at) VALUES ($1,$2,$3,$4,'lunch','pending',$5,$6,$7,'[]'::jsonb,$8)`, id, route, int64(access.Revision), int64(snapshot.CatalogRevision), snapshot.SchemaVersion, candidate, changeJSON, now); err != nil {
		return routewire.PendingProposal{}, err
	}
	return proposal, nil
}

func (q *Queries) ReadLunchProposal(ctx context.Context, routeID d.RouteID, owner d.UserID, proposalID d.ProposalID) (LunchProposalRecord, error) {
	var out LunchProposalRecord
	out.ID = proposalID
	var reason string
	var schema int
	var candidate, changes, conflicts []byte
	var resolved *time.Time
	var applied *d.RouteRevisionNumber
	err := q.db.QueryRow(ctx, `SELECT p.base_revision,p.base_catalog_revision,p.reason,p.state,p.candidate_schema_version,p.candidate,p.changes,p.conflicts,p.created_at,p.resolved_at,p.applied_revision FROM planning.route_proposal p JOIN planning.route r ON r.id=p.route_id WHERE p.route_id=$1 AND r.owner_id=$2 AND p.id=$3`, encodeUUID([16]byte(routeID)), encodeUUID([16]byte(owner)), encodeUUID([16]byte(proposalID))).Scan(&out.BaseRevision, &out.CatalogRevision, &reason, &out.State, &schema, &candidate, &changes, &conflicts, &out.CreatedAt, &resolved, &applied)
	if err != nil {
		return LunchProposalRecord{}, mapQueryError("read lunch proposal", err)
	}
	if reason != "lunch" || json.Unmarshal(candidate, &out.Candidate) != nil || out.Candidate.FormatVersion != 1 || out.Candidate.City == "" || out.Candidate.Plan.Validate() != nil || out.Candidate.Plan.SchemaVersion != schema || out.Candidate.Plan.CatalogRevision != out.CatalogRevision || out.BaseRevision <= 0 || out.CatalogRevision <= 0 || out.CreatedAt.IsZero() {
		return LunchProposalRecord{}, routewire.ErrInvalidResult
	}
	var wireChanges []routewire.ProposalChange
	var wireConflicts []routewire.Conflict
	if json.Unmarshal(changes, &wireChanges) != nil || json.Unmarshal(conflicts, &wireConflicts) != nil || len(wireConflicts) != 0 {
		return LunchProposalRecord{}, routewire.ErrInvalidResult
	}
	expected, err := routewire.ProposalChangesToWire(out.Candidate.Changes)
	if err != nil || !reflect.DeepEqual(expected, wireChanges) {
		return LunchProposalRecord{}, routewire.ErrInvalidResult
	}
	switch out.State {
	case d.ProposalPending:
		if resolved != nil || applied != nil {
			return LunchProposalRecord{}, routewire.ErrInvalidResult
		}
	case d.ProposalApplied:
		if resolved == nil || applied == nil || *applied <= out.BaseRevision {
			return LunchProposalRecord{}, routewire.ErrInvalidResult
		}
	case d.ProposalRejected, d.ProposalInvalidated:
		if resolved == nil || applied != nil {
			return LunchProposalRecord{}, routewire.ErrInvalidResult
		}
	default:
		return LunchProposalRecord{}, routewire.ErrInvalidResult
	}
	return out, nil
}

func (q *Queries) ApplyLunchProposal(ctx context.Context, routeID d.RouteID, owner d.UserID, expected d.RouteRevisionNumber, proposalID d.ProposalID, now time.Time) (d.RouteRevisionNumber, error) {
	if _, ok := q.db.(pgx.Tx); !ok || now.IsZero() {
		return 0, routewire.ErrInvalidRecomputeInput
	}
	hint, err := q.ReadLunchProposal(ctx, routeID, owner, proposalID)
	if err != nil {
		return 0, err
	}
	if hint.State != d.ProposalPending {
		return 0, ErrProposalNotPending
	}
	if err := q.LockCatalog(ctx, hint.Candidate.City, int64(hint.CatalogRevision)); err != nil {
		return 0, err
	}
	access, err := q.LockOwnedRoute(ctx, routeID, owner)
	if err != nil {
		return 0, err
	}
	if err := RequireRevision(access, expected); err != nil {
		return 0, err
	}
	if err := RequireRevision(access, hint.BaseRevision); err != nil {
		return 0, err
	}
	state, err := q.ReadRecomputeState(ctx, routeID, owner)
	if err != nil {
		return 0, err
	}
	if state.City != hint.Candidate.City || now.Before(hint.CreatedAt) {
		return 0, routewire.ErrInvalidResult
	}
	if err := q.checkPinHistory(ctx, routeID, state.Plan, hint.Candidate.History); err != nil {
		return 0, err
	}
	if _, err := routewire.BuildLunchRecompute(routeID, state.City, state.Plan, hint.Candidate.History, hint.Candidate.Intent); err != nil {
		return 0, err
	}
	snapshot := hint.Candidate.Plan.Clone()
	if err := routewire.ValidateLunchCandidate(state.Plan, snapshot, hint.Candidate.History, hint.Candidate.Intent, hint.Candidate.Changes); err != nil {
		return 0, err
	}
	if err := q.CheckCity(ctx, state.City, snapshot.Timezone); err != nil {
		return 0, err
	}
	future := snapshot.Clone()
	future.Steps = nil
	done := map[d.VisitID]bool{}
	for _, execution := range state.History {
		done[execution.VisitID] = execution.Status == d.ExecutionCompleted
	}
	for _, step := range snapshot.Steps {
		if !done[step.VisitID] {
			future.Steps = append(future.Steps, step)
		}
	}
	if err := q.CheckPlanCatalog(ctx, state.City, future); err != nil {
		return 0, err
	}
	next, err := q.saveRecomputeRevision(ctx, access, state.City, state.Plan, snapshot, "apply", proposalID, now)
	if err != nil {
		return 0, err
	}
	updated, err := q.db.Exec(ctx, `UPDATE planning.route_proposal SET state='applied',resolved_at=$3,applied_revision=$4 WHERE route_id=$1 AND id=$2 AND state='pending'`, encodeUUID([16]byte(routeID)), encodeUUID([16]byte(proposalID)), now, int64(next))
	if err != nil {
		return 0, err
	}
	if updated.RowsAffected() != 1 {
		return 0, ErrProposalNotPending
	}
	return next, nil
}

func (q *Queries) RejectLunchProposal(ctx context.Context, routeID d.RouteID, owner d.UserID, expected d.RouteRevisionNumber, proposalID d.ProposalID, now time.Time) (d.RouteRevisionNumber, error) {
	if _, ok := q.db.(pgx.Tx); !ok || now.IsZero() {
		return 0, routewire.ErrInvalidRecomputeInput
	}
	access, err := q.LockOwnedRoute(ctx, routeID, owner)
	if err != nil {
		return 0, err
	}
	if err := RequireRevision(access, expected); err != nil {
		return 0, err
	}
	proposal, err := q.ReadLunchProposal(ctx, routeID, owner, proposalID)
	if err != nil {
		return 0, err
	}
	if proposal.State != d.ProposalPending {
		return 0, ErrProposalNotPending
	}
	if err := RequireRevision(access, proposal.BaseRevision); err != nil {
		return 0, err
	}
	if now.Before(proposal.CreatedAt) {
		return 0, routewire.ErrInvalidResult
	}
	updated, err := q.db.Exec(ctx, `UPDATE planning.route_proposal SET state='rejected',resolved_at=$3 WHERE route_id=$1 AND id=$2 AND state='pending'`, encodeUUID([16]byte(routeID)), encodeUUID([16]byte(proposalID)), now)
	if err != nil {
		return 0, err
	}
	if updated.RowsAffected() != 1 {
		return 0, ErrProposalNotPending
	}
	return access.Revision, nil
}
