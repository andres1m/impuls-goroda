package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrInvalidShare = errors.New("invalid share command")

type ShareMutation struct {
	Share     domain.RouteShare
	Revision  domain.RouteRevisionNumber
	Unchanged bool
}

func (q *Queries) CreateOwnedShare(ctx context.Context, routeID domain.RouteID, actorID domain.UserID,
	expected domain.RouteRevisionNumber, hash [32]byte, expiresAt *time.Time, now time.Time,
) (ShareMutation, error) {
	if hash == ([32]byte{}) || now.IsZero() {
		return ShareMutation{}, ErrInvalidShare
	}
	access, err := q.LockOwnedRoute(ctx, routeID, actorID)
	if err != nil {
		return ShareMutation{}, err
	}
	if err := RequireRevision(access, expected); err != nil {
		return ShareMutation{}, err
	}
	current, err := q.lockCurrentShare(ctx, routeID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return ShareMutation{}, err
	}
	if err == nil && current.TokenHash == hash && current.ActiveAt(now) {
		return ShareMutation{Share: current, Revision: access.Revision, Unchanged: true}, nil
	}
	now = now.UTC().Truncate(time.Microsecond)
	var expiry *time.Time
	if expiresAt != nil {
		value := expiresAt.UTC().Truncate(time.Microsecond)
		if !value.After(now) {
			return ShareMutation{}, ErrInvalidShare
		}
		expiry = &value
	}
	if err == nil {
		if now.Before(current.CreatedAt) {
			return ShareMutation{}, ErrInvalidShare
		}
		if err := q.revokeCurrentShare(ctx, current.ID, now); err != nil {
			return ShareMutation{}, err
		}
	}
	id, err := uuid.NewV7()
	if err != nil {
		return ShareMutation{}, errors.New("create share identifier failed")
	}
	item := domain.RouteShare{ID: domain.ShareID(id), RouteID: routeID, CreatedBy: actorID,
		TokenHash: hash, CreatedAt: now, ExpiresAt: expiry}
	if err := item.Validate(); err != nil {
		return ShareMutation{}, ErrInvalidShare
	}
	_, err = q.db.Exec(ctx, `INSERT INTO planning.route_share
    (id, route_id, token_hash, created_by, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)`, encodeUUID([16]byte(item.ID)), encodeUUID([16]byte(routeID)),
		hash[:], encodeUUID([16]byte(actorID)), now, expiry)
	if err != nil {
		var constraint *pgconn.PgError
		if errors.As(err, &constraint) && constraint.Code == "23505" && constraint.ConstraintName == "route_share_token_hash_key" {
			return ShareMutation{}, ErrInvalidShare
		}
		return ShareMutation{}, fmt.Errorf("insert route share: %w", err)
	}
	return ShareMutation{Share: item, Revision: access.Revision}, nil
}

func (q *Queries) RevokeOwnedShare(ctx context.Context, routeID domain.RouteID, actorID domain.UserID,
	expected domain.RouteRevisionNumber, now time.Time,
) (domain.RouteRevisionNumber, error) {
	if now.IsZero() {
		return 0, ErrInvalidShare
	}
	access, err := q.LockOwnedRoute(ctx, routeID, actorID)
	if err != nil {
		return 0, err
	}
	if err := RequireRevision(access, expected); err != nil {
		return 0, err
	}
	item, err := q.lockCurrentShare(ctx, routeID)
	if errors.Is(err, ErrNotFound) {
		return access.Revision, nil
	}
	if err != nil {
		return 0, err
	}
	now = now.UTC().Truncate(time.Microsecond)
	if now.Before(item.CreatedAt) {
		return 0, ErrInvalidShare
	}
	if err := q.revokeCurrentShare(ctx, item.ID, now); err != nil {
		return 0, err
	}
	return access.Revision, nil
}

func (q *Queries) lockCurrentShare(ctx context.Context, routeID domain.RouteID) (domain.RouteShare, error) {
	var item domain.RouteShare
	var id, owner pgtype.UUID
	var hash []byte
	item.RouteID = routeID
	err := q.db.QueryRow(ctx, `SELECT id, token_hash, created_by, created_at, revoked_at, expires_at
FROM planning.route_share WHERE route_id = $1 AND revoked_at IS NULL FOR UPDATE`,
		encodeUUID([16]byte(routeID))).Scan(&id, &hash, &owner, &item.CreatedAt, &item.RevokedAt, &item.ExpiresAt)
	if err != nil {
		return domain.RouteShare{}, mapQueryError("lock route share", err)
	}
	shareID, err := decodeUUID(id)
	if err != nil {
		return domain.RouteShare{}, err
	}
	actorID, err := decodeUUID(owner)
	if err != nil {
		return domain.RouteShare{}, err
	}
	item.ID, item.CreatedBy = domain.ShareID(shareID), domain.UserID(actorID)
	item.TokenHash, err = decodeHash(hash)
	if err != nil {
		return domain.RouteShare{}, err
	}
	if err := item.Validate(); err != nil {
		return domain.RouteShare{}, errors.New("invalid stored route share")
	}
	return item, nil
}

func (q *Queries) revokeCurrentShare(ctx context.Context, shareID domain.ShareID, now time.Time) error {
	tag, err := q.db.Exec(ctx, `UPDATE planning.route_share SET revoked_at = $2
WHERE id = $1 AND revoked_at IS NULL`, encodeUUID([16]byte(shareID)), now)
	if err != nil {
		return fmt.Errorf("revoke route share: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}
