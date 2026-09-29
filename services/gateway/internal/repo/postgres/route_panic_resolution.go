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

type PanicProposalRecord struct {
	ID              d.ProposalID
	RouteID         d.RouteID
	BaseRevision    d.RouteRevisionNumber
	CatalogRevision d.CatalogRevision
	State           d.ProposalState
	CreatedAt       time.Time
	EffectiveStart  time.Time
	ResolvedAt      *time.Time
	AppliedRevision *d.RouteRevisionNumber
	Candidate       panicCandidate
}

func (q *Queries) ReadPanicProposal(ctx context.Context, routeID d.RouteID, owner d.UserID, proposalID d.ProposalID) (PanicProposalRecord, error) {
	return q.readPanicProposal(ctx, routeID, owner, proposalID, false)
}

func (q *Queries) readPanicProposal(ctx context.Context, routeID d.RouteID, owner d.UserID, proposalID d.ProposalID, lock bool) (PanicProposalRecord, error) {
	if routeID == (d.RouteID{}) || owner == (d.UserID{}) || proposalID == (d.ProposalID{}) {
		return PanicProposalRecord{}, ErrNotFound
	}
	query := `SELECT p.base_revision,p.base_catalog_revision,p.reason,p.state,p.candidate_schema_version,p.candidate,p.changes,p.conflicts,p.created_at,p.effective_start_at,p.resolved_at,p.applied_revision
FROM planning.route_proposal p JOIN planning.route r ON r.id=p.route_id
WHERE p.route_id=$1 AND r.owner_id=$2 AND p.id=$3`
	if lock {
		if _, ok := q.db.(pgx.Tx); !ok {
			return PanicProposalRecord{}, errors.New("proposal lock requires a transaction")
		}
		query += ` FOR UPDATE OF p`
	}
	out := PanicProposalRecord{ID: proposalID, RouteID: routeID}
	var reason string
	var schema int
	var candidate, changes, conflicts []byte
	var effective *time.Time
	if err := q.db.QueryRow(ctx, query, encodeUUID([16]byte(routeID)), encodeUUID([16]byte(owner)), encodeUUID([16]byte(proposalID))).Scan(&out.BaseRevision, &out.CatalogRevision, &reason, &out.State, &schema, &candidate, &changes, &conflicts, &out.CreatedAt, &effective, &out.ResolvedAt, &out.AppliedRevision); err != nil {
		return PanicProposalRecord{}, mapQueryError("read panic proposal", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(candidate))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out.Candidate); err != nil {
		return PanicProposalRecord{}, routewire.ErrInvalidResult
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return PanicProposalRecord{}, routewire.ErrInvalidResult
	}
	stored := out.Candidate
	if stored.FormatVersion != 1 || reason != "delay" || schema != stored.Plan.SchemaVersion || out.BaseRevision <= 0 || out.CatalogRevision <= 0 || stored.Plan.CatalogRevision != out.CatalogRevision || out.CreatedAt.IsZero() || stored.City == "" || stored.CalculatedAt.IsZero() || out.CreatedAt.Before(stored.CalculatedAt) || effective == nil || effective.IsZero() || stored.Input.RouteID != uuid.UUID(routeID).String() || stored.Plan.Validate() != nil || len(stored.Plan.Conflicts) != 0 || (stored.Plan.Result != d.ResultReady && stored.Plan.Result != d.ResultPartial) {
		return PanicProposalRecord{}, routewire.ErrInvalidResult
	}
	trigger, err := stored.Input.Proto(stored.CalculatedAt)
	if err != nil || !effective.Equal(trigger.EffectiveStartAt.AsTime()) {
		return PanicProposalRecord{}, routewire.ErrInvalidResult
	}
	out.EffectiveStart = *effective
	seen := make(map[d.VisitID]bool, len(stored.History))
	for _, execution := range stored.History {
		if execution.Validate() != nil || execution.RouteID != routeID || seen[execution.VisitID] || (execution.Status == d.ExecutionPlanned && (execution.ActualStartedAt != nil || execution.ActualEndedAt != nil)) || (execution.Status == d.ExecutionCompleted && (execution.ActualStartedAt == nil || execution.ActualEndedAt == nil || !execution.ActualEndedAt.After(*execution.ActualStartedAt))) {
			return PanicProposalRecord{}, routewire.ErrInvalidResult
		}
		seen[execution.VisitID] = true
	}
	if routewire.ValidatePanicPlanConnectivity(stored.Plan, stored.History, stored.Input, stored.CalculatedAt) != nil {
		return PanicProposalRecord{}, routewire.ErrInvalidResult
	}
	var wireChanges []routewire.ProposalChange
	var wireConflicts []routewire.Conflict
	if json.Unmarshal(changes, &wireChanges) != nil || json.Unmarshal(conflicts, &wireConflicts) != nil || wireChanges == nil || wireConflicts == nil || len(wireConflicts) != 0 {
		return PanicProposalRecord{}, routewire.ErrInvalidResult
	}
	expected, err := routewire.ProposalChangesToWire(stored.Changes)
	if err != nil || !reflect.DeepEqual(expected, wireChanges) {
		return PanicProposalRecord{}, routewire.ErrInvalidResult
	}
	switch out.State {
	case d.ProposalPending:
		if out.ResolvedAt != nil || out.AppliedRevision != nil {
			return PanicProposalRecord{}, routewire.ErrInvalidResult
		}
	case d.ProposalApplied:
		if out.ResolvedAt == nil || out.AppliedRevision == nil || *out.AppliedRevision <= out.BaseRevision {
			return PanicProposalRecord{}, routewire.ErrInvalidResult
		}
	case d.ProposalRejected, d.ProposalInvalidated:
		if out.ResolvedAt == nil || out.AppliedRevision != nil {
			return PanicProposalRecord{}, routewire.ErrInvalidResult
		}
	default:
		return PanicProposalRecord{}, routewire.ErrInvalidResult
	}
	if out.ResolvedAt != nil && out.ResolvedAt.Before(out.CreatedAt) {
		return PanicProposalRecord{}, routewire.ErrInvalidResult
	}
	return out, nil
}

func (q *Queries) RejectPanicProposal(ctx context.Context, routeID d.RouteID, owner d.UserID, expected d.RouteRevisionNumber, proposalID d.ProposalID, now time.Time) (d.RouteRevisionNumber, error) {
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
	proposal, err := q.readPanicProposal(ctx, routeID, owner, proposalID, true)
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

func (q *Queries) readAppliedResume(ctx context.Context, routeID d.RouteID, owner d.UserID, revision d.RouteRevisionNumber) (*d.RouteResume, error) {
	var proposalID uuid.UUID
	err := q.db.QueryRow(ctx, `WITH RECURSIVE ancestry AS (
SELECT v.revision,v.parent_revision,v.mutation_kind FROM planning.route_revision v
JOIN planning.route r ON r.id=v.route_id WHERE v.route_id=$1 AND r.owner_id=$2 AND v.revision=$3
UNION ALL
SELECT v.revision,v.parent_revision,v.mutation_kind FROM planning.route_revision v JOIN ancestry a ON v.route_id=$1 AND v.revision=a.parent_revision
WHERE a.mutation_kind IN ('save','participation','execution') AND v.revision<a.revision)
SELECT p.id FROM ancestry a JOIN planning.route_proposal p ON p.route_id=$1 AND p.applied_revision=a.revision
WHERE a.mutation_kind='apply' AND p.reason='delay' AND p.state='applied' ORDER BY a.revision DESC LIMIT 1`, encodeUUID([16]byte(routeID)), encodeUUID([16]byte(owner)), int64(revision)).Scan(&proposalID)
	if err != nil {
		return nil, mapQueryError("read applied resume", err)
	}
	proposal, err := q.ReadPanicProposal(ctx, routeID, owner, d.ProposalID(proposalID))
	if err != nil {
		return nil, err
	}
	position := 1
	for _, execution := range proposal.Candidate.History {
		if execution.Status == d.ExecutionCompleted {
			position++
		}
	}
	if position > len(proposal.Candidate.Plan.Legs) {
		return nil, routewire.ErrInvalidResult
	}
	leg := proposal.Candidate.Plan.Legs[position-1]
	return &d.RouteResume{LegPosition: position, DepartureAt: leg.DepartureAt, Position: d.Coordinate{Latitude: proposal.Candidate.Input.Position.Latitude, Longitude: proposal.Candidate.Input.Position.Longitude}}, nil
}

func (q *Queries) ApplyPanicProposal(ctx context.Context, routeID d.RouteID, owner d.UserID, expected d.RouteRevisionNumber, proposalID d.ProposalID, now time.Time) (d.RouteRevisionNumber, error) {
	if _, ok := q.db.(pgx.Tx); !ok || now.IsZero() {
		return 0, routewire.ErrInvalidRecomputeInput
	}
	hint, err := q.ReadPanicProposal(ctx, routeID, owner, proposalID)
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
	proposal, err := q.readPanicProposal(ctx, routeID, owner, proposalID, true)
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
	if _, err := routewire.BuildPanicRecompute(routeID, state.City, state.Plan, proposal.Candidate.History, proposal.Candidate.Input, proposal.Candidate.CalculatedAt); err != nil {
		return 0, err
	}
	snapshot := proposal.Candidate.Plan.Clone()
	if err := routewire.ValidatePanicCandidate(state.Plan, snapshot, proposal.Candidate.History, proposal.Candidate.Input, proposal.Candidate.CalculatedAt, proposal.Candidate.Changes); err != nil {
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
