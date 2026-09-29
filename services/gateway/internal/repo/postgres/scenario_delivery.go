package postgres

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrScenarioDeliveryLeaseLost = errors.New("scenario delivery lease is no longer owned")

type ScenarioDeliveryJob struct {
	ID         uuid.UUID
	OwnerID    d.UserID
	ScenarioID string
	Version    int64
	Attempts   int
	LeaseToken uuid.UUID
}

func (q *Queries) EnqueueScenarioResult(ctx context.Context, owner d.UserID, scenarioID string, version int64, now time.Time) error {
	if _, ok := q.db.(pgx.Tx); !ok || owner == (d.UserID{}) || !routewire.ValidScenarioID(scenarioID) || version < 1 || now.IsZero() {
		return errors.New("invalid scenario result enqueue")
	}
	if err := q.lockScenarioDeliveryOwner(ctx, owner); err != nil {
		return err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	_, err = q.db.Exec(ctx, `INSERT INTO gateway_ops.scenario_result_delivery
(id,owner_id,conversation_key,scenario_version,state,next_attempt_at,created_at)
SELECT $1,c.user_id,c.conversation_key,c.state_version,
CASE WHEN COALESCE(m.stopped,false) OR COALESCE(m.muted,false) THEN 'suppressed' ELSE 'pending' END,
CASE WHEN COALESCE(m.stopped,false) OR COALESCE(m.muted,false) THEN NULL ELSE $5::timestamptz END,$5
FROM gateway_ops.conversation c JOIN identity.user_account u ON u.id=c.user_id
LEFT JOIN gateway_ops.max_delivery_state m ON m.user_account_id=u.id
WHERE c.user_id=$2 AND c.conversation_key=$3 AND c.state_version=$4
AND (c.expires_at IS NULL OR c.expires_at>$5) AND u.account_kind='max' AND u.account_state='active'
AND c.pending_extraction #>> '{scenario,outcome,status}' IN ('READY','PARTIAL','NO_FEASIBLE_ROUTE','CONFLICT')
ON CONFLICT (owner_id,conversation_key,scenario_version) DO NOTHING`, id, encodeUUID([16]byte(owner)), scenarioKey(scenarioID), version, now)
	return err
}

func (q *Queries) ClaimScenarioResult(ctx context.Context, now time.Time) (*ScenarioDeliveryJob, error) {
	if now.IsZero() {
		return nil, errors.New("invalid scenario delivery clock")
	}
	if _, err := q.db.Exec(ctx, `UPDATE gateway_ops.scenario_result_delivery
SET state='failed',next_attempt_at=NULL,lease_token=NULL,lease_until=NULL
WHERE state='pending' AND attempts>=5 AND (lease_until IS NULL OR lease_until<=$1)`, now); err != nil {
		return nil, err
	}
	token, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	job := ScenarioDeliveryJob{LeaseToken: token}
	var owner uuid.UUID
	var key string
	err = q.db.QueryRow(ctx, `WITH due AS (
SELECT id FROM gateway_ops.scenario_result_delivery
WHERE state='pending' AND attempts<5 AND next_attempt_at<=$1 AND (lease_until IS NULL OR lease_until<=$1)
ORDER BY next_attempt_at,id LIMIT 1 FOR UPDATE SKIP LOCKED)
UPDATE gateway_ops.scenario_result_delivery j SET lease_token=$2,lease_until=$3,attempts=j.attempts+1
FROM due WHERE j.id=due.id RETURNING j.id,j.owner_id,j.conversation_key,j.scenario_version,j.attempts`, now, token, now.Add(30*time.Second)).Scan(&job.ID, &owner, &key, &job.Version, &job.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	job.OwnerID, job.ScenarioID = d.UserID(owner), strings.TrimPrefix(key, "scenario:")
	if !routewire.ValidScenarioID(job.ScenarioID) {
		return nil, errors.New("invalid stored scenario delivery")
	}
	return &job, nil
}

func (q *Queries) CheckScenarioResultDelivery(ctx context.Context, job ScenarioDeliveryJob, now time.Time) (int64, bool, error) {
	if _, ok := q.db.(pgx.Tx); !ok || now.IsZero() {
		return 0, false, errors.New("scenario delivery check requires a transaction")
	}
	var current bool
	err := q.db.QueryRow(ctx, `SELECT state_version=$3 AND (expires_at IS NULL OR expires_at>$4)
FROM gateway_ops.conversation WHERE user_id=$1 AND conversation_key=$2 FOR SHARE`, encodeUUID([16]byte(job.OwnerID)), scenarioKey(job.ScenarioID), job.Version, now).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	var owned bool
	err = q.db.QueryRow(ctx, `SELECT state='pending' AND lease_token=$2 AND lease_until>$3
FROM gateway_ops.scenario_result_delivery WHERE id=$1 AND owner_id=$4 AND conversation_key=$5 AND scenario_version=$6 FOR UPDATE`, job.ID, job.LeaseToken, now, encodeUUID([16]byte(job.OwnerID)), scenarioKey(job.ScenarioID), job.Version).Scan(&owned)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !owned {
		return 0, false, ErrScenarioDeliveryLeaseLost
	}
	if err != nil {
		return 0, false, err
	}
	if !current {
		return 0, false, nil
	}
	scenario, err := q.ReadBotScenario(ctx, job.OwnerID, job.ScenarioID)
	if errors.Is(err, ErrNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if scenario.Outcome == nil || scenario.Version != strconv.FormatInt(job.Version, 10) {
		return 0, false, nil
	}
	var maxID string
	var allowed bool
	err = q.db.QueryRow(ctx, `SELECT u.max_user_id,u.account_kind='max' AND u.account_state='active'
AND NOT COALESCE(m.stopped,false) AND NOT COALESCE(m.muted,false)
FROM identity.user_account u LEFT JOIN gateway_ops.max_delivery_state m ON m.user_account_id=u.id WHERE u.id=$1`, encodeUUID([16]byte(job.OwnerID))).Scan(&maxID, &allowed)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	id, parseErr := strconv.ParseInt(maxID, 10, 64)
	if !allowed || parseErr != nil || id <= 0 || strconv.FormatInt(id, 10) != maxID {
		return 0, false, nil
	}
	return id, true, nil
}

func (q *Queries) FinishScenarioResult(ctx context.Context, job ScenarioDeliveryJob, state string, next *time.Time, now time.Time) error {
	if _, ok := q.db.(pgx.Tx); !ok || now.IsZero() ||
		(state != "pending" && state != "sent" && state != "failed" && state != "suppressed") ||
		(state == "pending" && (next == nil || !next.After(now) || job.Attempts >= 5)) || (state != "pending" && next != nil) {
		return errors.New("invalid scenario delivery completion")
	}
	updated, err := q.db.Exec(ctx, `UPDATE gateway_ops.scenario_result_delivery SET state=$3,next_attempt_at=$4,
sent_at=CASE WHEN $3='sent' THEN $5 ELSE NULL END,lease_token=NULL,lease_until=NULL
WHERE id=$1 AND lease_token=$2 AND lease_until>$5 AND state='pending'`, job.ID, job.LeaseToken, state, next, now)
	if err != nil {
		return err
	}
	if updated.RowsAffected() != 1 {
		return ErrScenarioDeliveryLeaseLost
	}
	return nil
}

func (q *Queries) ScenarioDeliveryStorageReady(ctx context.Context) (bool, error) {
	var ready bool
	err := q.db.QueryRow(ctx, `WITH required(name) AS (VALUES ('gateway_ops.scenario_result_delivery'),('gateway_ops.max_delivery_state'))
SELECT NOT pg_is_in_recovery() AND current_setting('transaction_read_only')='off'
AND bool_and(COALESCE(has_schema_privilege(c.relnamespace,'USAGE') AND has_table_privilege(c.oid,'SELECT'),false))
AND COALESCE(has_table_privilege(to_regclass('gateway_ops.scenario_result_delivery'),'INSERT')
AND has_table_privilege(to_regclass('gateway_ops.scenario_result_delivery'),'UPDATE'),false)
AND EXISTS(SELECT 1 FROM pg_index WHERE indexrelid=to_regclass('gateway_ops.scenario_result_delivery_due_idx') AND indisvalid)
FROM required t LEFT JOIN pg_class c ON c.oid=to_regclass(t.name)`).Scan(&ready)
	return ready, err
}

func (q *Queries) lockScenarioDeliveryOwner(ctx context.Context, owner d.UserID) error {
	_, err := q.db.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('scenario-result:' || $1::uuid::text,0))`, encodeUUID([16]byte(owner)))
	return err
}
