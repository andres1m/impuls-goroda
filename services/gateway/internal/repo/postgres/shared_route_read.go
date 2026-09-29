package postgres

import (
	"context"
	"errors"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/sharewire"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (q *Queries) ReadSharedRoute(ctx context.Context, hash [32]byte) (sharewire.SharedRoute, error) {
	if _, ok := q.db.(pgx.Tx); !ok {
		return sharewire.SharedRoute{}, errors.New("shared route read requires a transaction")
	}
	var route d.Route
	var revision d.RouteRevision
	var id, owner pgtype.UUID
	err := q.db.QueryRow(ctx, `SELECT r.id,r.owner_id,r.city,r.lifecycle_state,r.current_revision,r.created_at,r.updated_at,
v.parent_revision,v.mutation_kind,v.created_at
FROM planning.route_share s JOIN planning.route r ON r.id=s.route_id
JOIN planning.route_revision v ON v.route_id=r.id AND v.revision=r.current_revision
WHERE s.token_hash=$1 AND s.revoked_at IS NULL
AND (s.expires_at IS NULL OR s.expires_at>transaction_timestamp())
AND (r.draft_expires_at IS NULL OR r.draft_expires_at>transaction_timestamp())`, hash[:]).Scan(
		&id, &owner, &route.City, &route.Lifecycle, &route.CurrentRevision, &route.CreatedAt, &route.UpdatedAt,
		&revision.Parent, &revision.Mutation, &revision.CreatedAt)
	if err != nil {
		return sharewire.SharedRoute{}, mapQueryError("read shared route", err)
	}
	routeID, err := decodeUUID(id)
	if err != nil {
		return sharewire.SharedRoute{}, err
	}
	ownerID, err := decodeUUID(owner)
	if err != nil {
		return sharewire.SharedRoute{}, err
	}
	route.ID, route.OwnerID = d.RouteID(routeID), d.UserID(ownerID)
	state, err := q.ReadRecomputeState(ctx, route.ID, route.OwnerID)
	if err != nil {
		return sharewire.SharedRoute{}, errors.New("shared route snapshot is unavailable")
	}
	if state.Access.Revision != route.CurrentRevision || state.City != route.City || state.Access.Lifecycle != route.Lifecycle {
		return sharewire.SharedRoute{}, errors.New("shared route snapshot is inconsistent")
	}
	revision.RouteID, revision.Number, revision.Plan = route.ID, route.CurrentRevision, state.Plan
	issues, err := q.readSharedIssues(ctx, route.ID)
	if err != nil {
		return sharewire.SharedRoute{}, err
	}
	return sharewire.ProjectRoute(route, revision, issues)
}

func (q *Queries) readSharedIssues(ctx context.Context, routeID d.RouteID) ([]d.RouteIssue, error) {
	rows, err := q.db.Query(ctx, `SELECT id,visit_id,issue_type,state,
COALESCE(details->>'code',details->>'Code'),COALESCE(details->>'message',details->>'Message'),
catalog_revision,created_at,resolved_at
FROM planning.route_issue WHERE route_id=$1 ORDER BY created_at,id`, encodeUUID([16]byte(routeID)))
	if err != nil {
		return nil, errors.New("shared route issues are unavailable")
	}
	defer rows.Close()
	out := make([]d.RouteIssue, 0)
	for rows.Next() {
		item := d.RouteIssue{RouteID: routeID}
		var id, visit pgtype.UUID
		if err := rows.Scan(&id, &visit, &item.Type, &item.State, &item.Details.Code, &item.Details.Message,
			&item.CatalogRevision, &item.CreatedAt, &item.ResolvedAt); err != nil {
			return nil, errors.New("invalid shared route issue")
		}
		decoded, err := decodeUUID(id)
		if err != nil {
			return nil, err
		}
		item.ID = d.IssueID(decoded)
		if visit.Valid {
			value := d.VisitID(visit.Bytes)
			item.VisitID = &value
		}
		if err := item.Validate(); err != nil {
			return nil, errors.New("invalid shared route issue")
		}
		out = append(out, item)
	}
	if rows.Err() != nil {
		return nil, errors.New("shared route issues are unavailable")
	}
	return out, nil
}
