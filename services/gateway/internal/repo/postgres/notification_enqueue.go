package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (q *Queries) EnqueueCancellationNotification(ctx context.Context, routeID, changeID uuid.UUID, now time.Time) error {
	if _, ok := q.db.(pgx.Tx); !ok || routeID == uuid.Nil || changeID == uuid.Nil || now.IsZero() {
		return errors.New("notification enqueue requires a valid transaction and cause")
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	_, err = q.db.Exec(ctx, `INSERT INTO gateway_ops.notification
(id,route_id,change_id,recipient_id,state,attempts,payload,next_attempt_at)
SELECT $1,r.id,$3,r.owner_id,'pending',0,jsonb_build_object('schema_version',1,'route_id',r.id::text),$4
FROM planning.route r JOIN gateway_ops.route_notification_preference p ON p.route_id=r.id AND p.owner_id=r.owner_id AND p.enabled
LEFT JOIN gateway_ops.max_delivery_state m ON m.user_account_id=r.owner_id
WHERE r.id=$2 AND r.lifecycle_state='saved' AND NOT COALESCE(m.stopped,false) AND NOT COALESCE(m.muted,false)
AND EXISTS(SELECT 1 FROM planning.route_issue i
JOIN planning.route_step s ON s.route_id=i.route_id AND s.revision=r.current_revision AND s.visit_id=i.visit_id
JOIN planning.route_visit v ON v.route_id=s.route_id AND v.visit_id=s.visit_id
JOIN catalog.session c ON c.city=v.city AND c.id=v.session_id AND c.availability_status='cancelled'
LEFT JOIN planning.execution e ON e.route_id=s.route_id AND e.visit_id=s.visit_id
WHERE i.route_id=r.id AND i.source_change_id=$3 AND i.issue_type='cancelled' AND i.state<>'resolved'
AND COALESCE(e.status,'planned')='planned')
ON CONFLICT(route_id,change_id,recipient_id) DO NOTHING`, id, routeID, changeID, now)
	return err
}
