package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"testing"
	"time"

	domain "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/lifecycle"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//nolint:cyclop // This test checks the complete transactional effect for two routes.
func TestSoldOutLifecycleAffectsOnlyUnconfirmedObligation(t *testing.T) {
	url := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("GATEWAY_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var city, dataMode string
	var sessionID, eventID, placeID uuid.UUID
	var start time.Time
	var catalogRevision int64
	err = tx.QueryRow(ctx, `SELECT s.city,s.id,s.event_id,e.place_id,s.starts_at,s.data_mode,c.catalog_revision
		FROM catalog.session s JOIN catalog.event e ON e.city=s.city AND e.id=s.event_id
		JOIN ref.city c ON c.code=s.city
		WHERE s.availability_status='sold_out' AND s.starts_at>now()+interval '1 hour'
		AND s.ends_at>s.starts_at+interval '20 minutes' LIMIT 1`).Scan(
		&city, &sessionID, &eventID, &placeID, &start, &dataMode, &catalogRevision,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		t.Skip("no future sold-out session in seeded catalog")
	}
	if err != nil {
		t.Fatal(err)
	}
	owner := uuid.New()
	_, err = tx.Exec(ctx, `INSERT INTO identity.user_account
		(id,max_user_id,created_at,last_seen_at,account_state,account_kind)
		VALUES($1,$2,now(),now(),'active','test')`, owner, "test:lifecycle:"+owner.String())
	if err != nil {
		t.Fatal(err)
	}
	affected := insertLifecycleTestRoute(
		t, ctx, tx, owner, city, sessionID, eventID, placeID, start, catalogRevision, false,
	)
	confirmed := insertLifecycleTestRoute(
		t, ctx, tx, owner, city, sessionID, eventID, placeID, start, catalogRevision, true,
	)
	past := insertLifecycleTestRoute(
		t, ctx, tx, owner, city, sessionID, eventID, placeID, time.Now().Add(-2*time.Hour), catalogRevision, false,
	)
	for _, route := range []uuid.UUID{affected, confirmed, past} {
		_, err = tx.Exec(ctx, `INSERT INTO gateway_ops.route_notification_preference
			(route_id,owner_id,enabled,version,changed_at) VALUES($1,$2,true,1,now())`, route, owner)
		if err != nil {
			t.Fatal(err)
		}
	}
	queries, err := NewQueries(tx)
	if err != nil {
		t.Fatal(err)
	}
	change := lifecycle.Change{
		SchemaVersion: 1, DeliveryID: uuid.New(), ChangeID: uuid.New(), City: city,
		CatalogRevision: catalogRevision, EventID: &eventID, SessionID: &sessionID,
		OldStatus: "available", NewStatus: "sold_out", DataMode: dataMode,
		ObservedAt: time.Now().UTC(), Reason: "source_status",
	}
	hash := sha256.Sum256([]byte("same delivery"))
	for range 2 {
		if err := queries.ReceiveCatalogLifecycle(ctx, change, hash, time.Now().UTC(), true, true); err != nil {
			t.Fatal(err)
		}
	}
	different := sha256.Sum256([]byte("different"))
	reuseErr := queries.ReceiveCatalogLifecycle(ctx, change, different, time.Now().UTC(), true, true)
	if !errors.Is(reuseErr, ErrLifecycleConflict) {
		t.Fatalf("reused delivery id: %v", reuseErr)
	}
	for route, want := range map[uuid.UUID]int{affected: 1, confirmed: 0, past: 0} {
		var issues, jobs, notifications int
		var revision int64
		issueSQL := `SELECT count(*) FROM planning.route_issue WHERE route_id=$1 AND source_change_id=$2`
		if err := tx.QueryRow(ctx, issueSQL, route, change.ChangeID).Scan(&issues); err != nil {
			t.Fatal(err)
		}
		jobSQL := `SELECT count(*) FROM gateway_ops.route_recompute_job WHERE route_id=$1 AND change_id=$2`
		if err := tx.QueryRow(ctx, jobSQL, route, change.ChangeID).Scan(&jobs); err != nil {
			t.Fatal(err)
		}
		notificationSQL := `SELECT count(*) FROM gateway_ops.notification WHERE route_id=$1 AND change_id=$2`
		if err := tx.QueryRow(ctx, notificationSQL, route, change.ChangeID).Scan(&notifications); err != nil {
			t.Fatal(err)
		}
		revisionSQL := `SELECT current_revision FROM planning.route WHERE id=$1`
		if err := tx.QueryRow(ctx, revisionSQL, route).Scan(&revision); err != nil {
			t.Fatal(err)
		}
		if issues != want || jobs != want || notifications != want || revision != 1 {
			t.Fatalf(
				"route %s: issues=%d jobs=%d notifications=%d revision=%d",
				route, issues, jobs, notifications, revision,
			)
		}
		visits, _, err := queries.CurrentCancellationVisits(ctx, CancellationJob{
			RouteID: domain.RouteID(route), OwnerID: domain.UserID(owner),
			MinCatalogRevision: domain.CatalogRevision(catalogRevision),
		})
		if err != nil || len(visits) != want {
			t.Fatalf("route %s: recompute visits=%d, err=%v", route, len(visits), err)
		}
	}
}

func insertLifecycleTestRoute(
	t *testing.T, ctx context.Context, tx pgx.Tx, owner uuid.UUID, city string,
	sessionID, eventID, placeID uuid.UUID, start time.Time, catalogRevision int64, confirmed bool,
) uuid.UUID {
	t.Helper()
	route, visit := uuid.New(), uuid.New()
	_, err := tx.Exec(ctx, `INSERT INTO planning.route
		(id,owner_id,city,lifecycle_state,current_revision,created_at,updated_at)
		VALUES($1,$2,$3,'saved',1,now(),now())`, route, owner, city)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO planning.route_revision
		(route_id,revision,lifecycle_state,archetype_id,timezone,start_at,end_at,origin,
		input_schema_version,constraints,catalog_revision,result_status,warnings,cost_summary,mutation_kind,created_at)
		SELECT $1,1,'saved','test',c.timezone,$2,$3,ST_SetSRID(ST_MakePoint(0,0),4326),
		1,'{}'::jsonb,$4,'READY','[]'::jsonb,'{}'::jsonb,'save',now()
		FROM ref.city c WHERE c.code=$5`, route, start.Add(-time.Hour), start.Add(time.Hour), catalogRevision, city)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO planning.route_visit
		(route_id,visit_id,visit_kind,city,place_id,event_id,session_id,created_in_revision,created_at)
		VALUES($1,$2,'visit',$3,$4,$5,$6,1,now())`, route, visit, city, placeID, eventID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO planning.route_step
		(route_id,revision,position,visit_id,arrival_at,visit_start_at,visit_end_at,departure_at,
		min_duration_s,is_pinned,is_obligation,participation_snapshot,catalog_snapshot,cost_snapshot,applied_constraints)
		VALUES($1,1,1,$2,$3,$3,$4,$4,0,false,true,'{}','{}','{}','{}')`, route, visit, start, start.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if confirmed {
		_, err = tx.Exec(ctx, `INSERT INTO planning.participation
			(route_id,visit_id,status,evidence_source,updated_in_revision,updated_at)
			VALUES($1,$2,'user_reported_confirmed','user',1,now())`, route, visit)
		if err != nil {
			t.Fatal(err)
		}
	}
	return route
}
