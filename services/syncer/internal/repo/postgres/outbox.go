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
	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/lifecycle"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/materialize"
)

// EnqueueRevision records in the publication's own transaction that caches still have to learn about
// the new revision, so the announcement cannot be lost between the commit and sending it.
func EnqueueRevision(ctx context.Context, tx pgx.Tx, m *catalogevent.Invalidation) error {
	payload, err := catalogevent.Encode(*m)
	if err != nil {
		return fmt.Errorf("encode catalog invalidation: %w", err)
	}
	change := uuid.New()
	_, err = tx.Exec(
		ctx,
		`
		INSERT INTO integration.change_delivery
			(id, change_id, city, catalog_revision, destination, event_type, payload, state, next_attempt_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', $8, $8)`,
		uuid.New(),
		change,
		m.City,
		m.CatalogRevision,
		catalogevent.Destination,
		catalogevent.EventType,
		payload,
		m.PublishedAt,
	)
	if err != nil {
		return fmt.Errorf("enqueue catalog revision: %w", err)
	}
	return nil
}

// enqueueCancellations records in the publication's own transaction that gateway and the urgent topic still
// have to hear about each cancelled session. The rows carry the revision that cancelled the session, so
// they exist only if the cancellation committed.
func enqueueCancellations(
	ctx context.Context,
	tx pgx.Tx,
	city domain.City,
	revision int64,
	at time.Time,
	gone []withdrawnSession,
) error {
	for i := range gone {
		g := &gone[i]
		c := lifecycle.Cancellation{
			Version:               lifecycle.SchemaVersion,
			ChangeID:              lifecycle.ChangeID(g.SessionID, revision),
			City:                  city,
			CatalogRevision:       revision,
			EventID:               g.EventID,
			SessionID:             g.SessionID,
			OldAvailabilityStatus: g.OldStatus,
			NewAvailabilityStatus: lifecycle.StatusCancelled,
			SourceRecordID:        g.SourceRecordID,
			DataMode:              g.DataMode,
			ObservedAt:            at,
			Reason:                lifecycle.ReasonSourceRemoved,
		}
		payload, err := lifecycle.Encode(&c)
		if err != nil {
			return fmt.Errorf("cancellation of session %s: %w", g.SessionID, err)
		}
		for _, target := range [...]struct{ destination, eventType string }{
			{lifecycle.GatewayDestination, lifecycle.GatewayEventType},
			{lifecycle.KafkaDestination, lifecycle.KafkaEventType},
		} {
			if _, err := tx.Exec(ctx, `
				INSERT INTO integration.change_delivery (id, change_id, city, catalog_revision, destination,
					event_type, source_record_id, payload, state, next_attempt_at, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pending', $9, $9)
				ON CONFLICT (change_id, destination, event_type) DO NOTHING`,
				uuid.New(), c.ChangeID, city, revision, target.destination, target.eventType, g.SourceRecordID,
				payload, at); err != nil {
				return fmt.Errorf("enqueue cancellation of session %s for %s: %w", g.SessionID, target.destination, err)
			}
		}
	}
	return nil
}

// deadLetterChanges derives a dead letter's change id from its raw record, so a repeated batch
// cannot queue the letter twice.
var deadLetterChanges = uuid.MustParse("5d0c7a3e-9b41-4f6e-8a2d-3c7b1e9f4a60")

// EnqueueDeadLetter records in the batch's transaction that a malformed raw record still has to reach
// the dead letter topic.
func EnqueueDeadLetter(
	ctx context.Context,
	tx pgx.Tx,
	city domain.City,
	raw *materialize.Raw,
	code string,
	at time.Time,
) error {
	payload, err := ingest.EncodeDeadLetter(&ingest.DeadLetter{
		Version:     ingest.DeadLetterVersion,
		Stage:       ingest.StagePayload,
		Reason:      domain.InvalidSchema,
		RawIngestID: raw.ID,
		Source:      raw.Source,
		City:        city,
		ExternalID:  raw.ExternalID,
		Error:       code,
	})
	if err != nil {
		return fmt.Errorf("dead letter for %s: %w", raw.ID, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO integration.change_delivery (id, change_id, city, destination, event_type, source_record_id,
			raw_ingest_id, payload, state, next_attempt_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6::uuid, $7::uuid, $8, 'pending', $9, $9)
		ON CONFLICT (change_id, destination, event_type) DO NOTHING`,
		uuid.New(), uuid.NewSHA1(deadLetterChanges, []byte(raw.ID)), city, ingest.DeadLetterDestination,
		ingest.DeadLetterEventType, raw.SourceRecordID, raw.ID, payload, at); err != nil {
		return fmt.Errorf("enqueue dead letter %s: %w", raw.ID, err)
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

func (d *Deliveries) Claim(
	ctx context.Context,
	now, leaseUntil time.Time,
	destinations []string,
	limit int,
) ([]delivery.Item, error) {
	rows, err := d.db.Query(ctx, claimSQL, now, leaseUntil, destinations, limit)
	if err != nil {
		return nil, fmt.Errorf("query outbox claims: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (delivery.Item, error) {
		var it delivery.Item
		scanErr := row.Scan(
			&it.ID,
			&it.Destination,
			&it.EventType,
			&it.Payload,
			&it.Attempts,
			&it.CreatedAt,
			&it.LeaseUntil,
		)
		if scanErr != nil {
			return it, fmt.Errorf("scan outbox claim: %w", scanErr)
		}
		return it, nil
	})
	if err != nil {
		return nil, fmt.Errorf("collect outbox claims: %w", err)
	}
	return items, nil
}

// ErrLeaseLost means the row's lease ended and another relay may have taken it, so the mark is not made.
var ErrLeaseLost = errors.New("outbox row is no longer leased to this relay")

func (d *Deliveries) Delivered(ctx context.Context, item *delivery.Item, at time.Time) error {
	return d.mark(ctx, `
		UPDATE integration.change_delivery SET state = 'delivered', delivered_at = $3, lease_until = NULL
		WHERE id = $1 AND state = 'in_flight' AND lease_until = $2`, item, at)
}

func (d *Deliveries) Failed(ctx context.Context, item *delivery.Item, next time.Time) error {
	return d.mark(ctx, `
		UPDATE integration.change_delivery SET state = 'failed', next_attempt_at = $3, lease_until = NULL
		WHERE id = $1 AND state = 'in_flight' AND lease_until = $2`, item, next)
}

func (d *Deliveries) mark(ctx context.Context, sql string, item *delivery.Item, at time.Time) error {
	tag, err := d.db.Exec(ctx, sql, item.ID, item.LeaseUntil, at)
	if err != nil {
		return fmt.Errorf("mark outbox item: %w", err)
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
	if err != nil {
		return count, 0, fmt.Errorf("query outbox backlog: %w", err)
	}
	if oldest == nil {
		return count, 0, nil
	}
	return count, max(0, now.Sub(*oldest)), nil
}
