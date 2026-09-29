package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrNotificationLeaseLost = errors.New("notification lease is no longer owned")

func (q *Queries) NotificationStorageReady(ctx context.Context) (bool, error) {
	var ready bool
	err := q.db.QueryRow(ctx, `WITH required(name) AS (VALUES
('gateway_ops.notification'),('gateway_ops.route_notification_preference'),('gateway_ops.max_delivery_state'))
SELECT NOT pg_is_in_recovery() AND current_setting('transaction_read_only')='off'
AND bool_and(COALESCE(has_schema_privilege(c.relnamespace,'USAGE') AND has_table_privilege(c.oid,'SELECT')
AND has_table_privilege(c.oid,'INSERT') AND has_table_privilege(c.oid,'UPDATE'),false))
AND (SELECT count(*)=4 FROM pg_attribute WHERE attrelid=to_regclass('gateway_ops.notification') AND NOT attisdropped
AND attname IN ('lease_token','lease_until','last_attempt_at','last_error_code'))
AND EXISTS(SELECT 1 FROM pg_index WHERE indexrelid=to_regclass('gateway_ops.notification_recipient_lease_idx') AND indisunique AND indisvalid)
FROM required t LEFT JOIN pg_class c ON c.oid=to_regclass(t.name)`).Scan(&ready)
	return ready, err
}

type NotificationJob struct {
	ID          uuid.UUID
	RouteID     d.RouteID
	RecipientID d.UserID
	ChangeID    uuid.UUID
	LeaseToken  uuid.UUID
	Attempts    int
	Payload     json.RawMessage
}

func (q *Queries) ClaimNotification(ctx context.Context, now time.Time) (*NotificationJob, error) {
	token, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	job := NotificationJob{LeaseToken: token}
	var route, recipient uuid.UUID
	err = q.db.QueryRow(ctx, `WITH due AS (
SELECT n.id FROM gateway_ops.notification n
WHERE n.state IN ('pending','failed') AND n.next_attempt_at<=$1 AND (n.lease_until IS NULL OR n.lease_until<=$1)
AND NOT EXISTS(SELECT 1 FROM gateway_ops.notification other WHERE other.recipient_id=n.recipient_id AND other.id<>n.id AND other.lease_token IS NOT NULL)
AND NOT EXISTS(SELECT 1 FROM gateway_ops.notification other WHERE other.recipient_id=n.recipient_id AND other.last_attempt_at>$1-interval '500 milliseconds')
ORDER BY n.next_attempt_at,n.id LIMIT 1 FOR UPDATE SKIP LOCKED)
UPDATE gateway_ops.notification n SET lease_token=$2,lease_until=$3,attempts=n.attempts+1,last_attempt_at=$1
FROM due WHERE n.id=due.id RETURNING n.id,n.route_id,n.recipient_id,n.change_id,n.attempts,n.payload`, now, token, now.Add(30*time.Second)).Scan(
		&job.ID, &route, &recipient, &job.ChangeID, &job.Attempts, &job.Payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	job.RouteID, job.RecipientID = d.RouteID(route), d.UserID(recipient)
	return &job, nil
}

func (q *Queries) CheckNotificationDelivery(ctx context.Context, job NotificationJob, now time.Time) (int64, bool, error) {
	if _, ok := q.db.(pgx.Tx); !ok {
		return 0, false, errors.New("notification check requires a transaction")
	}
	access, err := q.LockOwnedRoute(ctx, job.RouteID, job.RecipientID)
	if err != nil {
		return 0, false, err
	}
	var owned bool
	err = q.db.QueryRow(ctx, `SELECT COALESCE(state IN ('pending','failed') AND lease_token=$2 AND lease_until>$3,false)
FROM gateway_ops.notification WHERE id=$1 FOR UPDATE`, job.ID, job.LeaseToken, now).Scan(&owned)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !owned {
		return 0, false, ErrNotificationLeaseLost
	}
	if err != nil {
		return 0, false, err
	}
	if access.Lifecycle != d.RouteSaved {
		return 0, false, nil
	}
	var allowed bool
	var maxID string
	err = q.db.QueryRow(ctx, `SELECT u.max_user_id,
u.account_kind='max' AND u.account_state='active' AND COALESCE(p.enabled,false)
AND NOT COALESCE(m.stopped,false) AND NOT COALESCE(m.muted,false)
AND EXISTS(SELECT 1 FROM planning.route_issue i
JOIN planning.route_step s ON s.route_id=i.route_id AND s.revision=r.current_revision AND s.visit_id=i.visit_id
JOIN planning.route_visit v ON v.route_id=s.route_id AND v.visit_id=s.visit_id
JOIN catalog.session c ON c.city=v.city AND c.id=v.session_id AND c.availability_status='cancelled'
LEFT JOIN planning.execution e ON e.route_id=s.route_id AND e.visit_id=s.visit_id
WHERE i.route_id=r.id AND i.source_change_id=$3 AND i.issue_type='cancelled' AND i.state<>'resolved' AND COALESCE(e.status,'planned')='planned')
FROM planning.route r JOIN identity.user_account u ON u.id=r.owner_id
LEFT JOIN gateway_ops.route_notification_preference p ON p.route_id=r.id AND p.owner_id=r.owner_id
LEFT JOIN gateway_ops.max_delivery_state m ON m.user_account_id=r.owner_id
WHERE r.id=$1 AND r.owner_id=$2`, encodeUUID([16]byte(job.RouteID)), encodeUUID([16]byte(job.RecipientID)), job.ChangeID).Scan(&maxID, &allowed)
	if err != nil {
		return 0, false, err
	}
	if !allowed {
		return 0, false, nil
	}
	userID, err := strconv.ParseInt(maxID, 10, 64)
	if err != nil || userID <= 0 || strconv.FormatInt(userID, 10) != maxID {
		return 0, false, nil
	}
	return userID, true, nil
}

func (q *Queries) FinishNotification(ctx context.Context, job NotificationJob, state, code string, next *time.Time, now time.Time) error {
	if _, ok := q.db.(pgx.Tx); !ok || (state != "sent" && state != "failed" && state != "suppressed") || now.IsZero() || state != "failed" && next != nil {
		return errors.New("invalid notification completion")
	}
	updated, err := q.db.Exec(ctx, `UPDATE gateway_ops.notification SET state=$3,last_error_code=NULLIF($4,''),next_attempt_at=$5,
sent_at=CASE WHEN $3='sent' THEN $6 ELSE sent_at END,last_attempt_at=$6,lease_token=NULL,lease_until=NULL
WHERE id=$1 AND lease_token=$2 AND lease_until>$6 AND state IN ('pending','failed')`, job.ID, job.LeaseToken, state, code, next, now)
	if err != nil {
		return err
	}
	if updated.RowsAffected() != 1 {
		return ErrNotificationLeaseLost
	}
	return nil
}
