package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"time"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type cancellationCandidate struct {
	FormatVersion      int                          `json:"format_version"`
	City               string                       `json:"city"`
	VisitIDs           []d.VisitID                  `json:"visit_ids"`
	MinCatalogRevision d.CatalogRevision            `json:"min_catalog_revision"`
	History            []d.Execution                `json:"history"`
	Plan               d.RoutePlanSnapshot          `json:"plan"`
	Changes            []routewire.RecomputedChange `json:"changes"`
}

func (q *Queries) SaveCancellationProposal(ctx context.Context, state RecomputeState, visitIDs []d.VisitID, minCatalogRevision d.CatalogRevision, result routewire.RecomputedResult, now time.Time) (routewire.PendingProposal, error) {
	if result.Diagnostics.Status != "PROPOSED" || result.Candidate == nil || now.IsZero() {
		return routewire.PendingProposal{}, routewire.ErrInvalidResult
	}
	if _, err := routewire.BuildCancellationRecompute(state.Access.RouteID, state.City, state.Plan, state.History, visitIDs, minCatalogRevision); err != nil {
		return routewire.PendingProposal{}, err
	}
	snapshot := result.Candidate.Clone()
	if int64(snapshot.CatalogRevision) != result.Diagnostics.CatalogRevision {
		return routewire.PendingProposal{}, routewire.ErrInvalidResult
	}
	if err := routewire.ValidateCancellationCandidate(state.Access.RouteID, state.City, state.Plan, snapshot, state.History, visitIDs, minCatalogRevision, result.Changes); err != nil {
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
	proposal, err := routewire.PendingCancellationToWire(d.ProposalID(newID), access.Revision, snapshot.CatalogRevision, snapshot, changes, now)
	if err != nil {
		return routewire.PendingProposal{}, err
	}
	candidateJSON, err := json.Marshal(cancellationCandidate{FormatVersion: 1, City: state.City, VisitIDs: visitIDs, MinCatalogRevision: minCatalogRevision, History: state.History, Plan: snapshot, Changes: result.Changes})
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
VALUES ($1,$2,$3,$4,'cancel','pending',$5,$6,$7,'[]'::jsonb,$8)`, newID, route, int64(access.Revision), int64(snapshot.CatalogRevision), snapshot.SchemaVersion, candidateJSON, changesJSON, now); err != nil {
		return routewire.PendingProposal{}, err
	}
	return proposal, nil
}

type CancellationProposalRecord struct {
	ID              d.ProposalID
	RouteID         d.RouteID
	BaseRevision    d.RouteRevisionNumber
	CatalogRevision d.CatalogRevision
	State           d.ProposalState
	CreatedAt       time.Time
	ResolvedAt      *time.Time
	AppliedRevision *d.RouteRevisionNumber
	Candidate       cancellationCandidate
}

func (q *Queries) ReadCancellationProposal(ctx context.Context, routeID d.RouteID, owner d.UserID, proposalID d.ProposalID) (CancellationProposalRecord, error) {
	return q.readCancellationProposal(ctx, routeID, owner, proposalID, false)
}

func (q *Queries) readCancellationProposal(ctx context.Context, routeID d.RouteID, owner d.UserID, proposalID d.ProposalID, lock bool) (CancellationProposalRecord, error) {
	if routeID == (d.RouteID{}) || owner == (d.UserID{}) || proposalID == (d.ProposalID{}) {
		return CancellationProposalRecord{}, ErrNotFound
	}
	query := `SELECT p.base_revision,p.base_catalog_revision,p.reason,p.state,p.candidate_schema_version,p.candidate,p.changes,p.conflicts,p.created_at,p.resolved_at,p.applied_revision
FROM planning.route_proposal p JOIN planning.route r ON r.id=p.route_id
WHERE p.route_id=$1 AND r.owner_id=$2 AND p.id=$3`
	if lock {
		if _, ok := q.db.(pgx.Tx); !ok {
			return CancellationProposalRecord{}, errors.New("proposal lock requires a transaction")
		}
		query += ` FOR UPDATE OF p`
	}
	out := CancellationProposalRecord{ID: proposalID, RouteID: routeID}
	var reason string
	var schema int
	var candidate, changes, conflicts []byte
	if err := q.db.QueryRow(ctx, query, encodeUUID([16]byte(routeID)), encodeUUID([16]byte(owner)), encodeUUID([16]byte(proposalID))).Scan(&out.BaseRevision, &out.CatalogRevision, &reason, &out.State, &schema, &candidate, &changes, &conflicts, &out.CreatedAt, &out.ResolvedAt, &out.AppliedRevision); err != nil {
		return CancellationProposalRecord{}, mapQueryError("read cancellation proposal", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(candidate))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out.Candidate); err != nil {
		return CancellationProposalRecord{}, routewire.ErrInvalidResult
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return CancellationProposalRecord{}, routewire.ErrInvalidResult
	}
	if out.Candidate.FormatVersion != 1 || reason != "cancel" || schema != out.Candidate.Plan.SchemaVersion || out.BaseRevision <= 0 || out.CatalogRevision <= 0 || out.Candidate.Plan.CatalogRevision != out.CatalogRevision || out.CreatedAt.IsZero() || out.Candidate.City == "" || out.Candidate.Plan.Validate() != nil || out.Candidate.MinCatalogRevision.Validate() != nil || out.CatalogRevision < out.Candidate.MinCatalogRevision || len(out.Candidate.VisitIDs) == 0 {
		return CancellationProposalRecord{}, routewire.ErrInvalidResult
	}
	seen := make(map[d.VisitID]bool, len(out.Candidate.VisitIDs))
	for _, id := range out.Candidate.VisitIDs {
		if id == (d.VisitID{}) || seen[id] {
			return CancellationProposalRecord{}, routewire.ErrInvalidResult
		}
		seen[id] = true
	}
	var wireChanges []routewire.ProposalChange
	var wireConflicts []routewire.Conflict
	if json.Unmarshal(changes, &wireChanges) != nil || json.Unmarshal(conflicts, &wireConflicts) != nil || wireChanges == nil || wireConflicts == nil || len(wireConflicts) != 0 {
		return CancellationProposalRecord{}, routewire.ErrInvalidResult
	}
	expected, err := routewire.ProposalChangesToWire(out.Candidate.Changes)
	if err != nil || !reflect.DeepEqual(expected, wireChanges) {
		return CancellationProposalRecord{}, routewire.ErrInvalidResult
	}
	switch out.State {
	case d.ProposalPending:
		if out.ResolvedAt != nil || out.AppliedRevision != nil {
			return CancellationProposalRecord{}, routewire.ErrInvalidResult
		}
	case d.ProposalApplied:
		if out.ResolvedAt == nil || out.AppliedRevision == nil || *out.AppliedRevision <= out.BaseRevision {
			return CancellationProposalRecord{}, routewire.ErrInvalidResult
		}
	case d.ProposalRejected, d.ProposalInvalidated:
		if out.ResolvedAt == nil || out.AppliedRevision != nil {
			return CancellationProposalRecord{}, routewire.ErrInvalidResult
		}
	default:
		return CancellationProposalRecord{}, routewire.ErrInvalidResult
	}
	if out.ResolvedAt != nil && out.ResolvedAt.Before(out.CreatedAt) {
		return CancellationProposalRecord{}, routewire.ErrInvalidResult
	}
	return out, nil
}

func (q *Queries) ApplyCancellationProposal(ctx context.Context, routeID d.RouteID, owner d.UserID, expected d.RouteRevisionNumber, proposalID d.ProposalID, now time.Time) (d.RouteRevisionNumber, error) {
	if _, ok := q.db.(pgx.Tx); !ok || now.IsZero() {
		return 0, routewire.ErrInvalidRecomputeInput
	}
	hint, err := q.ReadCancellationProposal(ctx, routeID, owner, proposalID)
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
	proposal, err := q.readCancellationProposal(ctx, routeID, owner, proposalID, true)
	if err != nil {
		return 0, err
	}
	if proposal.State != d.ProposalPending {
		return 0, ErrProposalNotPending
	}
	if err := RequireRevision(access, proposal.BaseRevision); err != nil {
		return 0, err
	}
	if !reflect.DeepEqual(hint, proposal) || now.Before(proposal.CreatedAt) {
		return 0, routewire.ErrInvalidResult
	}
	state, err := q.ReadRecomputeState(ctx, routeID, owner)
	if err != nil {
		return 0, err
	}
	if state.City != proposal.Candidate.City {
		return 0, routewire.ErrInvalidResult
	}
	if err := q.checkPinHistory(ctx, routeID, state.Plan, proposal.Candidate.History); err != nil {
		return 0, err
	}
	if _, err := routewire.BuildCancellationRecompute(routeID, state.City, state.Plan, proposal.Candidate.History, proposal.Candidate.VisitIDs, proposal.Candidate.MinCatalogRevision); err != nil {
		return 0, err
	}
	snapshot := proposal.Candidate.Plan.Clone()
	if err := routewire.ValidateCancellationCandidate(routeID, state.City, state.Plan, snapshot, proposal.Candidate.History, proposal.Candidate.VisitIDs, proposal.Candidate.MinCatalogRevision, proposal.Candidate.Changes); err != nil {
		return 0, err
	}
	if err := q.CheckCity(ctx, state.City, snapshot.Timezone); err != nil {
		return 0, err
	}
	done := make(map[d.VisitID]bool, len(proposal.Candidate.History))
	for _, execution := range proposal.Candidate.History {
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

func (q *Queries) RejectCancellationProposal(ctx context.Context, routeID d.RouteID, owner d.UserID, expected d.RouteRevisionNumber, proposalID d.ProposalID, now time.Time) (d.RouteRevisionNumber, error) {
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
	proposal, err := q.readCancellationProposal(ctx, routeID, owner, proposalID, true)
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
	if err := q.ExplainCancellationProblem(ctx, routeID, proposal.Candidate.VisitIDs, proposal.CatalogRevision, "CANCELLATION_PROPOSAL_REJECTED", "Сеанс отменён. Предложение отклонено; расписание не изменено. Вы можете убрать отменённое посещение из маршрута."); err != nil {
		return 0, err
	}
	return access.Revision, nil
}
