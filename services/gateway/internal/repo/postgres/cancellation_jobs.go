package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrCancellationLeaseLost = errors.New("cancellation job lease is no longer owned")

type CancellationJob struct {
	ID                 uuid.UUID
	RouteID            d.RouteID
	OwnerID            d.UserID
	City               string
	MinCatalogRevision d.CatalogRevision
	Attempts           int
	LeaseToken         uuid.UUID
	LeaseUntil         time.Time
}

func (q *Queries) CancellationStorageReady(ctx context.Context) (bool, error) {
	var ready bool
	err := q.db.QueryRow(ctx, `SELECT NOT pg_is_in_recovery() AND current_setting('transaction_read_only')='off'
AND COALESCE((SELECT has_schema_privilege(c.relnamespace,'USAGE')
AND has_table_privilege(c.oid,'SELECT') AND has_table_privilege(c.oid,'INSERT')
AND has_table_privilege(c.oid,'UPDATE') FROM pg_class c
WHERE c.oid=to_regclass('gateway_ops.route_recompute_job')),false)`).Scan(&ready)
	return ready, err
}

func (q *Queries) EnqueueCancellationJob(ctx context.Context, routeID, changeID uuid.UUID, city string, revision int64, now time.Time) error {
	if _, ok := q.db.(pgx.Tx); !ok {
		return errors.New("cancellation enqueue requires a transaction")
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	_, err = q.db.Exec(ctx, `INSERT INTO gateway_ops.route_recompute_job
(id,route_id,city,change_id,min_catalog_revision,state,next_attempt_at,created_at)
SELECT $1,$2,$3,$4,$5,'pending',$6,$6 WHERE EXISTS(
SELECT 1 FROM planning.route_issue i JOIN planning.route r ON r.id=i.route_id
JOIN planning.route_step s ON s.route_id=r.id AND s.revision=r.current_revision AND s.visit_id=i.visit_id
LEFT JOIN planning.execution e ON e.route_id=s.route_id AND e.visit_id=s.visit_id
WHERE i.route_id=$2 AND i.source_change_id=$4 AND i.issue_type='cancelled' AND i.state<>'resolved'
AND s.visit_end_at>$6 AND COALESCE(e.status,'planned')='planned') ON CONFLICT(route_id,change_id) DO NOTHING`, id, routeID, city, changeID, revision, now)
	return err
}

func (q *Queries) ClaimCancellationJob(ctx context.Context, now time.Time) (*CancellationJob, error) {
	token, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	job := CancellationJob{LeaseToken: token}
	var route, owner uuid.UUID
	err = q.db.QueryRow(ctx, `WITH due AS (
SELECT j.id FROM gateway_ops.route_recompute_job j WHERE
((j.state='pending' AND j.next_attempt_at<=$1) OR (j.state='running' AND j.lease_until<=$1))
AND NOT EXISTS(SELECT 1 FROM gateway_ops.route_recompute_job other WHERE other.route_id=j.route_id AND other.state='running' AND other.id<>j.id)
ORDER BY j.next_attempt_at,j.created_at,j.id LIMIT 1 FOR UPDATE SKIP LOCKED)
UPDATE gateway_ops.route_recompute_job j SET state='running',attempts=j.attempts+1,lease_token=$2,lease_until=$3
FROM due WHERE j.id=due.id RETURNING j.id,j.route_id,(SELECT owner_id FROM planning.route WHERE id=j.route_id),j.city,j.min_catalog_revision,j.attempts,j.lease_until`, now, token, now.Add(30*time.Second)).Scan(&job.ID, &route, &owner, &job.City, &job.MinCatalogRevision, &job.Attempts, &job.LeaseUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	job.RouteID, job.OwnerID = d.RouteID(route), d.UserID(owner)
	return &job, nil
}

func (q *Queries) CurrentCancellationVisits(ctx context.Context, job CancellationJob) ([]d.VisitID, d.CatalogRevision, error) {
	rows, err := q.db.Query(ctx, `SELECT i.visit_id,i.catalog_revision FROM planning.route_issue i
JOIN planning.route r ON r.id=i.route_id AND r.owner_id=$2
JOIN planning.route_step s ON s.route_id=r.id AND s.revision=r.current_revision AND s.visit_id=i.visit_id
JOIN planning.route_visit v ON v.route_id=s.route_id AND v.visit_id=s.visit_id
LEFT JOIN planning.route_lunch_step lunch ON lunch.route_id=s.route_id AND lunch.revision=s.revision AND lunch.visit_id=s.visit_id
JOIN catalog.session c ON c.city=v.city AND c.id=CASE WHEN lunch.visit_id IS NULL THEN v.session_id ELSE lunch.session_id END
LEFT JOIN planning.participation p ON p.route_id=v.route_id AND p.visit_id=v.visit_id
LEFT JOIN planning.execution e ON e.route_id=v.route_id AND e.visit_id=v.visit_id
WHERE i.route_id=$1 AND i.issue_type='cancelled' AND i.state<>'resolved' AND COALESCE(e.status,'planned')='planned'
AND s.visit_end_at>now()
AND (c.availability_status='cancelled' OR (c.availability_status='sold_out' AND s.is_obligation
AND COALESCE(p.status,'not_required') NOT IN ('user_reported_confirmed','provider_confirmed')))
ORDER BY s.position,i.id`, encodeUUID([16]byte(job.RouteID)), encodeUUID([16]byte(job.OwnerID)))
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	seen := make(map[d.VisitID]bool)
	visits := make([]d.VisitID, 0)
	minimum := job.MinCatalogRevision
	for rows.Next() {
		var id uuid.UUID
		var revision d.CatalogRevision
		if err := rows.Scan(&id, &revision); err != nil {
			return nil, 0, err
		}
		visit := d.VisitID(id)
		if !seen[visit] {
			visits = append(visits, visit)
			seen[visit] = true
		}
		minimum = max(minimum, revision)
	}
	return visits, minimum, rows.Err()
}

func (q *Queries) LockCancellationJob(ctx context.Context, job CancellationJob, now time.Time) error {
	if _, ok := q.db.(pgx.Tx); !ok {
		return errors.New("cancellation job lock requires a transaction")
	}
	var owned bool
	err := q.db.QueryRow(ctx, `SELECT COALESCE(state='running' AND lease_token=$2 AND lease_until>$3,false)
FROM gateway_ops.route_recompute_job WHERE id=$1 FOR UPDATE`, job.ID, job.LeaseToken, now).Scan(&owned)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !owned {
		return ErrCancellationLeaseLost
	}
	return err
}

func (q *Queries) FinishCancellationJob(ctx context.Context, job CancellationJob, result json.RawMessage, now time.Time) error {
	if _, ok := q.db.(pgx.Tx); !ok {
		return errors.New("cancellation completion requires a transaction")
	}
	if !json.Valid(result) {
		return errors.New("invalid cancellation result")
	}
	updated, err := q.db.Exec(ctx, `UPDATE gateway_ops.route_recompute_job SET state='done',result=$3,
completed_at=$4,lease_token=NULL,lease_until=NULL,last_error_code=NULL WHERE id=$1 AND state='running' AND lease_token=$2 AND lease_until>$4`, job.ID, job.LeaseToken, result, now)
	if err != nil {
		return err
	}
	if updated.RowsAffected() != 1 {
		return ErrCancellationLeaseLost
	}
	return nil
}

func (q *Queries) RetryCancellationJob(ctx context.Context, job CancellationJob, now time.Time, code string) error {
	if _, ok := q.db.(pgx.Tx); !ok {
		return errors.New("cancellation retry requires a transaction")
	}
	delay := time.Second
	for i := 1; i < job.Attempts && delay < time.Minute; i++ {
		delay = min(2*delay, time.Minute)
	}
	updated, err := q.db.Exec(ctx, `UPDATE gateway_ops.route_recompute_job SET state='pending',next_attempt_at=$3,
lease_token=NULL,lease_until=NULL,last_error_code=$4 WHERE id=$1 AND state='running' AND lease_token=$2 AND lease_until>$5`, job.ID, job.LeaseToken, now.Add(delay), code, now)
	if err != nil {
		return err
	}
	if updated.RowsAffected() != 1 {
		return ErrCancellationLeaseLost
	}
	return nil
}

func (q *Queries) ExplainCancellationProblem(ctx context.Context, routeID d.RouteID, visits []d.VisitID, revision d.CatalogRevision, code, message string) error {
	if _, ok := q.db.(pgx.Tx); !ok {
		return errors.New("cancellation explanation requires a transaction")
	}
	details, err := json.Marshal(map[string]string{"code": code, "message": message})
	if err != nil {
		return err
	}
	for _, visit := range visits {
		if _, err := q.db.Exec(ctx, `UPDATE planning.route_issue SET details=$3 WHERE route_id=$1 AND visit_id=$2
AND issue_type='cancelled' AND state<>'resolved' AND catalog_revision<=$4`, encodeUUID([16]byte(routeID)), encodeUUID([16]byte(visit)), details, int64(revision)); err != nil {
			return err
		}
	}
	return nil
}
