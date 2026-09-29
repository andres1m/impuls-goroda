package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/lifecycle"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrLifecycleConflict = errors.New("catalog lifecycle payload does not match")

//nolint:gocognit,gocritic,cyclop,funlen // Inbox, catalog check and route effects must commit together.
func (q *Queries) ReceiveCatalogLifecycle(
	ctx context.Context,
	change lifecycle.Change,
	hash [32]byte,
	now time.Time,
	enqueue, notify bool,
) error {
	if _, ok := q.db.(pgx.Tx); !ok || now.IsZero() || change.SchemaVersion != 1 ||
		change.DeliveryID == uuid.Nil ||
		change.ChangeID == uuid.Nil ||
		change.City == "" ||
		change.CatalogRevision <= 0 ||
		change.EventID == nil && change.SessionID == nil ||
		hash == ([32]byte{}) {
		return lifecycle.ErrInvalidChange
	}
	id := change.DeliveryID.String()
	if _, err := q.db.Exec(ctx, `INSERT INTO gateway_ops.inbox(producer,event_id,payload_hash,state,received_at)
VALUES('syncer_catalog',$1,$2,'received',$3) ON CONFLICT(producer,event_id) DO NOTHING`, id, hash[:], now); err != nil {
		return err
	}
	var storedHash, result []byte
	var state string
	if err := q.db.QueryRow(ctx, `SELECT payload_hash,state,result_ref FROM gateway_ops.inbox
WHERE producer='syncer_catalog' AND event_id=$1 FOR UPDATE`, id).Scan(&storedHash, &state, &result); err != nil {
		return err
	}
	if !bytes.Equal(hash[:], storedHash) {
		return ErrLifecycleConflict
	}
	if state == "processed" {
		var ack struct {
			DeliveryID string `json:"delivery_id"`
		}
		if json.Unmarshal(result, &ack) != nil || ack.DeliveryID != id {
			return errors.New("stored catalog lifecycle acknowledgement is invalid")
		}
		return nil
	}
	var catalogRevision int64
	if err := q.db.QueryRow(ctx, `SELECT ref.lock_city_shared($1)`, change.City).Scan(&catalogRevision); err != nil {
		return err
	}
	if catalogRevision < change.CatalogRevision {
		return ErrCatalogChanged
	}
	var present bool
	var current string
	if change.SessionID != nil {
		if err := q.db.QueryRow(ctx, `SELECT availability_status FROM catalog.session
WHERE city=$1 AND id=$2 AND ($3::uuid IS NULL OR event_id=$3)`, change.City, change.SessionID, change.EventID).Scan(&current); err != nil {
			return mapQueryError("read lifecycle session", err)
		}
	} else {
		if err := q.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM catalog.event WHERE city=$1 AND id=$2)`, change.City, change.EventID).Scan(&present); err != nil {
			return err
		}
		if !present {
			return ErrNotFound
		}
	}
	if change.SessionID != nil && current != change.NewStatus {
		return q.finishLifecycleInbox(ctx, id, now, "superseded")
	}
	rows, err := q.db.Query(ctx, `SELECT r.id FROM planning.route r WHERE r.city=$1 AND EXISTS(
SELECT 1 FROM planning.route_step s JOIN planning.route_visit v ON v.route_id=s.route_id AND v.visit_id=s.visit_id
WHERE s.route_id=r.id AND s.revision=r.current_revision
AND ($2::uuid IS NULL OR v.session_id=$2) AND ($3::uuid IS NULL OR v.event_id=$3)) ORDER BY r.id FOR UPDATE OF r`, change.City, change.SessionID, change.EventID)
	if err != nil {
		return err
	}
	routes, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	issueType, code, message := "stale", "CATALOG_VISIT_CHANGED", "Данные посещения обновились. Проверьте условия."
	if current == "cancelled" && change.NewStatus == current {
		issueType, code, message = "cancelled", "SESSION_CANCELLED", "Сеанс отменён. Расписание ещё не изменено."
	} else if current == "sold_out" && change.NewStatus == current {
		issueType, code, message = "cancelled", "SESSION_SOLD_OUT", "На обязательное посещение больше нет мест. Расписание ещё не изменено."
	}
	details, err := json.Marshal(map[string]string{"code": code, "message": message})
	if err != nil {
		return err
	}
	for _, route := range routes {
		if current == change.NewStatus &&
			(current == "available" || current == "registration_required") {
			if _, err := q.db.Exec(ctx, `UPDATE planning.route_issue i SET state='resolved',resolved_at=$5
FROM planning.route r,planning.route_step s,planning.route_visit v
WHERE r.id=$1 AND s.route_id=r.id AND s.revision=r.current_revision AND v.route_id=s.route_id AND v.visit_id=s.visit_id
AND i.route_id=r.id AND i.visit_id=v.visit_id AND i.issue_type='cancelled' AND i.state<>'resolved'
AND ($2::uuid IS NULL OR v.session_id=$2) AND ($3::uuid IS NULL OR v.event_id=$3) AND i.catalog_revision<=$4`, route, change.SessionID, change.EventID, catalogRevision, now); err != nil {
				return err
			}
		}
		issueArgs := []any{
			route, change.SessionID, change.EventID, change.ChangeID, issueType,
			details, catalogRevision, now, change.NewStatus,
		}
		created, err := q.db.Exec(
			ctx,
			`INSERT INTO planning.route_issue(id,route_id,visit_id,source_change_id,issue_type,details,state,catalog_revision,created_at)
SELECT gen_random_uuid(),r.id,v.visit_id,$4,$5,$6,'open',$7,$8
FROM planning.route r JOIN planning.route_step s ON s.route_id=r.id AND s.revision=r.current_revision
JOIN planning.route_visit v ON v.route_id=s.route_id AND v.visit_id=s.visit_id
LEFT JOIN planning.execution e ON e.route_id=v.route_id AND e.visit_id=v.visit_id
LEFT JOIN planning.participation p ON p.route_id=v.route_id AND p.visit_id=v.visit_id
WHERE r.id=$1 AND ($2::uuid IS NULL OR v.session_id=$2) AND ($3::uuid IS NULL OR v.event_id=$3)
AND r.lifecycle_state='saved' AND s.visit_end_at>$8 AND COALESCE(e.status,'planned')='planned'
AND ($9::text<>'sold_out' OR (s.is_obligation AND COALESCE(p.status,'not_required') NOT IN ('user_reported_confirmed','provider_confirmed')))
ON CONFLICT DO NOTHING`,
			issueArgs...)
		if err != nil {
			return err
		}
		if notify && issueType == "cancelled" && created.RowsAffected() > 0 {
			if err := q.EnqueueCancellationNotification(ctx, route, change.ChangeID, now); err != nil {
				return err
			}
		}
		if enqueue && issueType == "cancelled" && created.RowsAffected() > 0 {
			if err := q.EnqueueCancellationJob(ctx, route, change.ChangeID, change.City, catalogRevision, now); err != nil {
				return err
			}
		}
	}
	return q.finishLifecycleInbox(ctx, id, now, "processed")
}

func (q *Queries) finishLifecycleInbox(ctx context.Context, id string, now time.Time, result string) error {
	ack, err := json.Marshal(map[string]string{"delivery_id": id, "result": result})
	if err != nil {
		return err
	}
	_, err = q.db.Exec(
		ctx,
		`UPDATE gateway_ops.inbox SET state='processed',result_ref=$2,processed_at=$3
WHERE producer='syncer_catalog' AND event_id=$1`,
		id,
		ack,
		now,
	)
	return err
}
