package postgres

import (
	"context"
	"errors"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (q *Queries) ReadOwnerRouteWithProposal(ctx context.Context, routeID d.RouteID, owner d.UserID) (routewire.OwnerRoute, error) {
	if _, ok := q.db.(pgx.Tx); !ok {
		return routewire.OwnerRoute{}, errors.New("owner proposal read requires a transaction")
	}
	route, err := q.ReadOwnerRoute(ctx, routeID, owner)
	if err != nil {
		return routewire.OwnerRoute{}, err
	}
	var proposalID uuid.UUID
	var reason string
	err = q.db.QueryRow(ctx, `SELECT p.id,p.reason FROM planning.route_proposal p
JOIN planning.route r ON r.id=p.route_id
WHERE r.id=$1 AND r.owner_id=$2 AND p.state='pending' AND p.base_revision=r.current_revision
ORDER BY p.created_at DESC,p.id DESC LIMIT 1`, encodeUUID([16]byte(routeID)), encodeUUID([16]byte(owner))).Scan(&proposalID, &reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return route, nil
	}
	if err != nil {
		return routewire.OwnerRoute{}, mapQueryError("read pending owner proposal", err)
	}
	state, err := q.ReadRecomputeState(ctx, routeID, owner)
	if err != nil {
		return routewire.OwnerRoute{}, err
	}
	var proposal routewire.PendingProposal
	switch reason {
	case "cancel":
		record, readErr := q.ReadCancellationProposal(ctx, routeID, owner, d.ProposalID(proposalID))
		if readErr != nil {
			return routewire.OwnerRoute{}, readErr
		}
		if record.State != d.ProposalPending || record.BaseRevision != state.Access.Revision || record.Candidate.City != state.City {
			return routewire.OwnerRoute{}, routewire.ErrInvalidResult
		}
		if err := routewire.ValidateCancellationCandidate(routeID, state.City, state.Plan, record.Candidate.Plan, state.History, record.Candidate.VisitIDs, record.Candidate.MinCatalogRevision, record.Candidate.Changes); err != nil {
			return routewire.OwnerRoute{}, err
		}
		changes, changeErr := routewire.ProposalChangesToWire(record.Candidate.Changes)
		if changeErr != nil {
			return routewire.OwnerRoute{}, changeErr
		}
		proposal, err = routewire.PendingCancellationToWire(record.ID, record.BaseRevision, record.CatalogRevision, record.Candidate.Plan, changes, record.CreatedAt)
	case "delete":
		record, readErr := q.ReadRemovalProposal(ctx, routeID, owner, d.ProposalID(proposalID))
		if readErr != nil {
			return routewire.OwnerRoute{}, readErr
		}
		visit, parseErr := uuid.Parse(record.Candidate.VisitID)
		if parseErr != nil || record.State != d.ProposalPending || record.BaseRevision != state.Access.Revision || record.Candidate.City != state.City {
			return routewire.OwnerRoute{}, routewire.ErrInvalidResult
		}
		if err := routewire.ValidateRemovalCandidate(state.Plan, record.Candidate.Plan, state.History, d.VisitID(visit), record.Candidate.Input, record.Candidate.Changes); err != nil {
			return routewire.OwnerRoute{}, err
		}
		changes, changeErr := routewire.ProposalChangesToWire(record.Candidate.Changes)
		if changeErr != nil {
			return routewire.OwnerRoute{}, changeErr
		}
		proposal, err = routewire.PendingRemovalToWire(record.ID, record.BaseRevision, record.CatalogRevision, record.Candidate.Plan, changes, record.CreatedAt)
	case "delay":
		record, readErr := q.ReadPanicProposal(ctx, routeID, owner, d.ProposalID(proposalID))
		if readErr != nil {
			return routewire.OwnerRoute{}, readErr
		}
		if record.State != d.ProposalPending || record.BaseRevision != state.Access.Revision || record.Candidate.City != state.City {
			return routewire.OwnerRoute{}, routewire.ErrInvalidResult
		}
		if err := routewire.ValidatePanicCandidate(state.Plan, record.Candidate.Plan, state.History, record.Candidate.Input, record.Candidate.CalculatedAt, record.Candidate.Changes); err != nil {
			return routewire.OwnerRoute{}, err
		}
		changes, changeErr := routewire.ProposalChangesToWire(record.Candidate.Changes)
		if changeErr != nil {
			return routewire.OwnerRoute{}, changeErr
		}
		proposal, err = routewire.PendingPanicToWire(record.ID, record.BaseRevision, record.CatalogRevision, record.Candidate.Plan, changes, record.CreatedAt, record.EffectiveStart)
	case "lunch":
		record, readErr := q.ReadLunchProposal(ctx, routeID, owner, d.ProposalID(proposalID))
		if readErr != nil {
			return routewire.OwnerRoute{}, readErr
		}
		if record.State != d.ProposalPending || record.BaseRevision != state.Access.Revision || record.Candidate.City != state.City {
			return routewire.OwnerRoute{}, routewire.ErrInvalidResult
		}
		if err := routewire.ValidateLunchCandidate(state.Plan, record.Candidate.Plan, state.History, record.Candidate.Intent, record.Candidate.Changes); err != nil {
			return routewire.OwnerRoute{}, err
		}
		changes, changeErr := routewire.ProposalChangesToWire(record.Candidate.Changes)
		if changeErr != nil {
			return routewire.OwnerRoute{}, changeErr
		}
		proposal, err = routewire.PendingLunchToWire(record.ID, record.BaseRevision, record.CatalogRevision, record.Candidate.Plan, changes, record.CreatedAt)
	default:
		return routewire.OwnerRoute{}, routewire.ErrInvalidResult
	}
	if err != nil {
		return routewire.OwnerRoute{}, err
	}
	route.PendingProposal = &proposal
	return route, nil
}
