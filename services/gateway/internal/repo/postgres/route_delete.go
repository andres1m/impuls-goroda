package postgres

import (
	"context"
	"errors"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/jackc/pgx/v5"
)

func (q *Queries) DeleteOwnedRoute(ctx context.Context, id domain.RouteID, actor domain.UserID, revision domain.RouteRevisionNumber, acknowledge bool) error {
	if _, ok := q.db.(pgx.Tx); !ok {
		return errors.New("route deletion requires a transaction")
	}
	access, err := q.LockOwnedRoute(ctx, id, actor)
	if err != nil {
		return err
	}
	if err := RequireRevision(access, revision); err != nil {
		return err
	}
	if !acknowledge {
		var committed bool
		if err := q.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM planning.participation
WHERE route_id=$1 AND status IN ('user_reported_confirmed','provider_confirmed'))`, encodeUUID([16]byte(id))).Scan(&committed); err != nil {
			return mapQueryError("read route commitments", err)
		}
		if committed {
			return routewire.ErrExternalCommitmentAcknowledgementRequired
		}
	}
	var deleted bool
	if err := q.db.QueryRow(ctx, `SELECT planning.purge_route($1,$2)`, encodeUUID([16]byte(id)), encodeUUID([16]byte(actor))).Scan(&deleted); err != nil {
		return mapQueryError("delete owned route", err)
	}
	if !deleted {
		return ErrNotFound
	}
	return nil
}
