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

var ErrProposalNotPending = errors.New("proposal is not pending")

func (q *Queries) ReadProposalReason(ctx context.Context, routeID d.RouteID, owner d.UserID, proposalID d.ProposalID) (string, error) {
	var reason string
	if err := q.db.QueryRow(ctx, `SELECT p.reason FROM planning.route_proposal p JOIN planning.route r ON r.id=p.route_id WHERE p.route_id=$1 AND r.owner_id=$2 AND p.id=$3`, encodeUUID([16]byte(routeID)), encodeUUID([16]byte(owner)), encodeUUID([16]byte(proposalID))).Scan(&reason); err != nil {
		return "", mapQueryError("read proposal reason", err)
	}
	return reason, nil
}

type RemovalProposalRecord struct {
	ID              d.ProposalID
	RouteID         d.RouteID
	BaseRevision    d.RouteRevisionNumber
	CatalogRevision d.CatalogRevision
	State           d.ProposalState
	CreatedAt       time.Time
	ResolvedAt      *time.Time
	AppliedRevision *d.RouteRevisionNumber
	Candidate       removalCandidate
}

func (q *Queries) ReadRemovalProposal(ctx context.Context, routeID d.RouteID, owner d.UserID, proposalID d.ProposalID) (RemovalProposalRecord, error) {
	return q.readRemovalProposal(ctx, routeID, owner, proposalID, false)
}

func (q *Queries) readRemovalProposal(ctx context.Context, routeID d.RouteID, owner d.UserID, proposalID d.ProposalID, lock bool) (RemovalProposalRecord, error) {
	if routeID == (d.RouteID{}) || owner == (d.UserID{}) || proposalID == (d.ProposalID{}) {
		return RemovalProposalRecord{}, ErrNotFound
	}
	query := `SELECT p.base_revision,p.base_catalog_revision,p.reason,p.state,p.candidate_schema_version,p.candidate,p.changes,p.conflicts,p.created_at,p.resolved_at,p.applied_revision
FROM planning.route_proposal p JOIN planning.route r ON r.id=p.route_id
WHERE p.route_id=$1 AND r.owner_id=$2 AND p.id=$3`
	if lock {
		if _, ok := q.db.(pgx.Tx); !ok {
			return RemovalProposalRecord{}, errors.New("proposal lock requires a transaction")
		}
		query += ` FOR UPDATE OF p`
	}
	out := RemovalProposalRecord{ID: proposalID, RouteID: routeID}
	var reason string
	var schema int
	var candidate, changes, conflicts []byte
	if err := q.db.QueryRow(ctx, query, encodeUUID([16]byte(routeID)), encodeUUID([16]byte(owner)), encodeUUID([16]byte(proposalID))).Scan(&out.BaseRevision, &out.CatalogRevision, &reason, &out.State, &schema, &candidate, &changes, &conflicts, &out.CreatedAt, &out.ResolvedAt, &out.AppliedRevision); err != nil {
		return RemovalProposalRecord{}, mapQueryError("read removal proposal", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(candidate))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out.Candidate); err != nil {
		return RemovalProposalRecord{}, routewire.ErrInvalidResult
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return RemovalProposalRecord{}, routewire.ErrInvalidResult
	}
	visit, err := uuid.Parse(out.Candidate.VisitID)
	if err != nil || visit == uuid.Nil || visit.String() != out.Candidate.VisitID || out.Candidate.FormatVersion != 1 || reason != "delete" || schema != out.Candidate.Plan.SchemaVersion || out.BaseRevision <= 0 || out.CatalogRevision <= 0 || out.Candidate.Plan.CatalogRevision != out.CatalogRevision || out.CreatedAt.IsZero() || out.Candidate.City == "" || out.Candidate.Plan.Validate() != nil {
		return RemovalProposalRecord{}, routewire.ErrInvalidResult
	}
	if _, err := out.Candidate.Input.Proto(d.VisitID(visit)); err != nil {
		return RemovalProposalRecord{}, routewire.ErrInvalidResult
	}
	var wireChanges []routewire.ProposalChange
	var wireConflicts []routewire.Conflict
	if json.Unmarshal(changes, &wireChanges) != nil || json.Unmarshal(conflicts, &wireConflicts) != nil || wireChanges == nil || wireConflicts == nil || len(wireConflicts) != 0 {
		return RemovalProposalRecord{}, routewire.ErrInvalidResult
	}
	expected, err := routewire.ProposalChangesToWire(out.Candidate.Changes)
	if err != nil || !reflect.DeepEqual(expected, wireChanges) {
		return RemovalProposalRecord{}, routewire.ErrInvalidResult
	}
	switch out.State {
	case d.ProposalPending:
		if out.ResolvedAt != nil || out.AppliedRevision != nil {
			return RemovalProposalRecord{}, routewire.ErrInvalidResult
		}
	case d.ProposalApplied:
		if out.ResolvedAt == nil || out.AppliedRevision == nil || *out.AppliedRevision <= out.BaseRevision {
			return RemovalProposalRecord{}, routewire.ErrInvalidResult
		}
	case d.ProposalRejected, d.ProposalInvalidated:
		if out.ResolvedAt == nil || out.AppliedRevision != nil {
			return RemovalProposalRecord{}, routewire.ErrInvalidResult
		}
	default:
		return RemovalProposalRecord{}, routewire.ErrInvalidResult
	}
	if out.ResolvedAt != nil && out.ResolvedAt.Before(out.CreatedAt) {
		return RemovalProposalRecord{}, routewire.ErrInvalidResult
	}
	return out, nil
}

func (q *Queries) ApplyRemovalProposal(ctx context.Context, routeID d.RouteID, owner d.UserID, expected d.RouteRevisionNumber, proposalID d.ProposalID, now time.Time) (d.RouteRevisionNumber, error) {
	if _, ok := q.db.(pgx.Tx); !ok || now.IsZero() {
		return 0, routewire.ErrInvalidRecomputeInput
	}
	hint, err := q.ReadRemovalProposal(ctx, routeID, owner, proposalID)
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
	proposal, err := q.readRemovalProposal(ctx, routeID, owner, proposalID, true)
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
	visit, _ := uuid.Parse(proposal.Candidate.VisitID)
	if _, err := routewire.BuildRemovalRecompute(routeID, state.City, state.Plan, proposal.Candidate.History, d.VisitID(visit), proposal.Candidate.Input); err != nil {
		return 0, err
	}
	snapshot := proposal.Candidate.Plan.Clone()
	if err := routewire.ValidateRemovalCandidate(state.Plan, snapshot, proposal.Candidate.History, d.VisitID(visit), proposal.Candidate.Input, proposal.Candidate.Changes); err != nil {
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

func (q *Queries) RejectRemovalProposal(ctx context.Context, routeID d.RouteID, owner d.UserID, expected d.RouteRevisionNumber, proposalID d.ProposalID, now time.Time) (d.RouteRevisionNumber, error) {
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
	proposal, err := q.readRemovalProposal(ctx, routeID, owner, proposalID, true)
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
