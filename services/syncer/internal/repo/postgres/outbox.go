package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/andres1m/impuls-goroda/pkg/catalogevent"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/delivery"
)

// EnqueueRevision records in the publication's own transaction that caches still have to learn about
// the new revision, so the announcement cannot be lost between the commit and sending it.
func EnqueueRevision(ctx context.Context, tx pgx.Tx, m catalogevent.Invalidation) error {
	payload, err := catalogevent.Encode(m)
	if err != nil {
		return fmt.Errorf("encode catalog invalidation: %w", err)
	}
	change := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO integration.change_delivery
			(id, change_id, city, catalog_revision, destination, event_type, payload, state, next_attempt_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', $8, $8)`,
		uuid.New(), change, m.City, m.CatalogRevision, catalogevent.Destination, catalogevent.EventType, payload, m.PublishedAt)
	if err != nil {
		return fmt.Errorf("enqueue catalog revision: %w", err)
	}
	return nil
}

type DeliveryDB interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Deliveries is the outbox as the delivery relay sees it.
type Deliveries struct {
	db DeliveryDB
}

func NewDeliveries(db DeliveryDB) *Deliveries {
	return &Deliveries{db: db}
}

const claimSQL = `
	WITH due AS (
		SELECT id FROM integration.change_delivery
		WHERE destination = ANY($3)
			AND ((state IN ('pending', 'failed') AND next_attempt_at <= $1)
				OR (state = 'in_flight' AND lease_until < $1))
		ORDER BY next_attempt_at, created_at
		LIMIT $4
		FOR UPDATE SKIP LOCKED
	)
	UPDATE integration.change_delivery d
	SET state = 'in_flight', lease_until = $2, attempts = d.attempts + 1
	FROM due WHERE d.id = due.id
	RETURNING d.id, d.destination, d.event_type, d.payload, d.attempts, d.created_at, d.lease_until`

func (d *Deliveries) Claim(ctx context.Context, now, leaseUntil time.Time, destinations []string, limit int) ([]delivery.Item, error) {
	rows, err := d.db.Query(ctx, claimSQL, now, leaseUntil, destinations, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (delivery.Item, error) {
		var it delivery.Item
		err := row.Scan(&it.ID, &it.Destination, &it.EventType, &it.Payload, &it.Attempts, &it.CreatedAt, &it.LeaseUntil)
		return it, err
	})
}

// ErrLeaseLost means the row's lease ended and another relay may have taken it, so the mark is not made.
var ErrLeaseLost = errors.New("outbox row is no longer leased to this relay")

func (d *Deliveries) Delivered(ctx context.Context, item delivery.Item, at time.Time) error {
	return d.mark(ctx, `
		UPDATE integration.change_delivery SET state = 'delivered', delivered_at = $3, lease_until = NULL
		WHERE id = $1 AND state = 'in_flight' AND lease_until = $2`, item, at)
}

func (d *Deliveries) Failed(ctx context.Context, item delivery.Item, next time.Time) error {
	return d.mark(ctx, `
		UPDATE integration.change_delivery SET state = 'failed', next_attempt_at = $3, lease_until = NULL
		WHERE id = $1 AND state = 'in_flight' AND lease_until = $2`, item, next)
}

func (d *Deliveries) mark(ctx context.Context, sql string, item delivery.Item, at time.Time) error {
	tag, err := d.db.Exec(ctx, sql, item.ID, item.LeaseUntil, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (d *Deliveries) Backlog(ctx context.Context, now time.Time) (int, time.Duration, error) {
	var (
		count  int
		oldest *time.Time
	)
	err := d.db.QueryRow(ctx, `
		SELECT count(*), min(created_at) FROM integration.change_delivery
		WHERE state IN ('pending', 'in_flight', 'failed')`).Scan(&count, &oldest)
	if err != nil || oldest == nil {
		return count, 0, err
	}
	return count, max(0, now.Sub(*oldest)), nil
}
