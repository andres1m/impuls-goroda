package postgres

import (
	"context"
	"errors"
	"fmt"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/jackc/pgx/v5"
)

type MAXDeliveryAxis string

const (
	MAXDeliveryStopped MAXDeliveryAxis = "stopped"
	MAXDeliveryMuted   MAXDeliveryAxis = "muted"
)

type MAXDeliveryObservation struct {
	UserID      d.UserID
	Axis        MAXDeliveryAxis
	Restricted  bool
	TimestampMS int64
}

func (q *Queries) ObserveMAXDeliveryState(ctx context.Context, input MAXDeliveryObservation) (bool, error) {
	if _, ok := q.db.(pgx.Tx); !ok || input.UserID == (d.UserID{}) || input.TimestampMS <= 0 {
		return false, errors.New("invalid MAX delivery observation")
	}
	var column string
	switch input.Axis {
	case MAXDeliveryStopped:
		column = "stopped"
	case MAXDeliveryMuted:
		column = "muted"
	default:
		return false, errors.New("invalid MAX delivery axis")
	}
	query := fmt.Sprintf(`INSERT INTO gateway_ops.max_delivery_state AS current(user_account_id,%[1]s,%[1]s_observed_at_ms)
VALUES($1,$2,$3) ON CONFLICT(user_account_id) DO UPDATE SET
%[1]s=CASE WHEN current.%[1]s_observed_at_ms=$3 THEN current.%[1]s OR $2 ELSE $2 END,
%[1]s_observed_at_ms=$3
WHERE current.%[1]s_observed_at_ms IS NULL OR current.%[1]s_observed_at_ms<$3
OR (current.%[1]s_observed_at_ms=$3 AND $2 AND NOT current.%[1]s)
RETURNING true`, column)
	if q.scenarioResultDelivery {
		if err := q.lockScenarioDeliveryOwner(ctx, input.UserID); err != nil {
			return false, mapQueryError("lock scenario result delivery", err)
		}
	}
	var applied bool
	err := q.db.QueryRow(ctx, query, encodeUUID([16]byte(input.UserID)), input.Restricted, input.TimestampMS).Scan(&applied)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, mapQueryError("observe MAX delivery state", err)
	}
	_, err = q.db.Exec(ctx, `UPDATE gateway_ops.notification SET state='suppressed',lease_token=NULL,lease_until=NULL,
last_error_code='MAX_DELIVERY_RESTRICTED'
WHERE recipient_id=$1 AND state IN ('pending','failed')
AND EXISTS(SELECT 1 FROM gateway_ops.max_delivery_state WHERE user_account_id=$1 AND (stopped OR muted))`,
		encodeUUID([16]byte(input.UserID)))
	if err != nil {
		return false, mapQueryError("suppress MAX delivery", err)
	}
	if q.scenarioResultDelivery {
		_, err = q.db.Exec(ctx, `UPDATE gateway_ops.scenario_result_delivery
SET state='suppressed',next_attempt_at=NULL,lease_token=NULL,lease_until=NULL
WHERE owner_id=$1 AND state='pending'
AND EXISTS(SELECT 1 FROM gateway_ops.max_delivery_state WHERE user_account_id=$1 AND (stopped OR muted))`, encodeUUID([16]byte(input.UserID)))
		if err != nil {
			return false, mapQueryError("suppress scenario result delivery", err)
		}
	}
	return applied, nil
}
