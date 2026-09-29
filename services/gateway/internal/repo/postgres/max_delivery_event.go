package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type MAXDeliveryEvent struct {
	ID          string
	Hash        [32]byte
	MAXUserID   int64
	Axis        MAXDeliveryAxis
	Restricted  bool
	TimestampMS int64
}

func (q *Queries) MAXDeliveryEventsStorageReady(ctx context.Context) (bool, error) {
	ready, err := q.NotificationStorageReady(ctx)
	if err != nil || !ready {
		return false, err
	}
	err = q.db.QueryRow(ctx, `SELECT COALESCE(has_schema_privilege(relnamespace,'USAGE')
AND has_table_privilege(oid,'SELECT') AND has_table_privilege(oid,'INSERT') AND has_table_privilege(oid,'UPDATE'),false)
FROM pg_class WHERE oid=to_regclass('gateway_ops.max_pending_delivery_state')`).Scan(&ready)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return ready, err
}

func (q *Queries) lockMAXPendingState(ctx context.Context, maxID int64) error {
	if _, ok := q.db.(pgx.Tx); !ok || maxID <= 0 {
		return errors.New("invalid MAX pending state transaction")
	}
	if _, err := q.db.Exec(ctx, `INSERT INTO gateway_ops.max_pending_delivery_state(max_user_id) VALUES($1)
ON CONFLICT(max_user_id) DO NOTHING`, maxID); err != nil {
		return err
	}
	var id int64
	return q.db.QueryRow(ctx, `SELECT max_user_id FROM gateway_ops.max_pending_delivery_state WHERE max_user_id=$1 FOR UPDATE`, maxID).Scan(&id)
}

func (q *Queries) bindMAXDeliveryState(ctx context.Context, maxID int64, userID d.UserID) error {
	var stopped, muted bool
	var stoppedAt, mutedAt pgtype.Int8
	if err := q.db.QueryRow(ctx, `SELECT stopped,stopped_observed_at_ms,muted,muted_observed_at_ms
FROM gateway_ops.max_pending_delivery_state WHERE max_user_id=$1`, maxID).Scan(&stopped, &stoppedAt, &muted, &mutedAt); err != nil {
		return err
	}
	for _, observation := range []struct {
		axis       MAXDeliveryAxis
		restricted bool
		at         pgtype.Int8
	}{
		{MAXDeliveryStopped, stopped, stoppedAt}, {MAXDeliveryMuted, muted, mutedAt},
	} {
		if observation.at.Valid {
			if _, err := q.ObserveMAXDeliveryState(ctx, MAXDeliveryObservation{UserID: userID, Axis: observation.axis,
				Restricted: observation.restricted, TimestampMS: observation.at.Int64}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (q *Queries) ReceiveMAXDeliveryEvent(ctx context.Context, event MAXDeliveryEvent, now time.Time) error {
	if _, ok := q.db.(pgx.Tx); !ok || event.MAXUserID <= 0 || event.TimestampMS <= 0 || len(event.ID) != 64 || event.Hash == ([32]byte{}) || now.IsZero() {
		return errors.New("invalid MAX delivery event")
	}
	var column string
	switch event.Axis {
	case MAXDeliveryStopped:
		column = "stopped"
	case MAXDeliveryMuted:
		column = "muted"
	default:
		return errors.New("invalid MAX delivery axis")
	}
	if _, err := q.db.Exec(ctx, `INSERT INTO gateway_ops.inbox(producer,event_id,payload_hash,state,received_at)
VALUES('max_delivery',$1,$2,'received',$3) ON CONFLICT(producer,event_id) DO NOTHING`, event.ID, event.Hash[:], now); err != nil {
		return err
	}
	var storedHash []byte
	var state string
	if err := q.db.QueryRow(ctx, `SELECT payload_hash,state FROM gateway_ops.inbox WHERE producer='max_delivery' AND event_id=$1 FOR UPDATE`, event.ID).Scan(&storedHash, &state); err != nil {
		return err
	}
	if !bytes.Equal(storedHash, event.Hash[:]) {
		return ErrBotEventConflict
	}
	if state == "processed" {
		return nil
	}
	if err := q.lockMAXPendingState(ctx, event.MAXUserID); err != nil {
		return err
	}
	query := fmt.Sprintf(`UPDATE gateway_ops.max_pending_delivery_state SET
%[1]s=CASE WHEN %[1]s_observed_at_ms=$3 THEN %[1]s OR $2 ELSE $2 END,%[1]s_observed_at_ms=$3
WHERE max_user_id=$1 AND (%[1]s_observed_at_ms IS NULL OR %[1]s_observed_at_ms<$3
OR (%[1]s_observed_at_ms=$3 AND $2 AND NOT %[1]s))`, column)
	if _, err := q.db.Exec(ctx, query, event.MAXUserID, event.Restricted, event.TimestampMS); err != nil {
		return err
	}
	var account pgtype.UUID
	err := q.db.QueryRow(ctx, `SELECT id FROM identity.user_account WHERE max_user_id=$1 AND account_kind='max'`, strconv.FormatInt(event.MAXUserID, 10)).Scan(&account)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil {
		if err := q.bindMAXDeliveryState(ctx, event.MAXUserID, d.UserID(account.Bytes)); err != nil {
			return err
		}
	}
	_, err = q.db.Exec(ctx, `UPDATE gateway_ops.inbox SET state='processed',processed_at=$2 WHERE producer='max_delivery' AND event_id=$1`, event.ID, now)
	return err
}
