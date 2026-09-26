package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCommandExecutorIntegration(t *testing.T) {
	databaseURL := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("GATEWAY_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	transactor, err := NewTransactor(pool)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := NewCommandExecutor(transactor)
	if err != nil {
		t.Fatal(err)
	}
	actorID := domain.UserID(randomID(t))
	routeID := domain.RouteID(randomID(t))
	now := time.Now().UTC()
	account := domain.UserAccount{
		ID: actorID, MaxUserID: fmt.Sprintf("test:commands:%x", actorID),
		State: domain.AccountActive, Kind: domain.AccountTest, CreatedAt: now, LastSeenAt: now,
	}
	queries, err := NewQueries(pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queries.UpsertTestAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	if err := transactor.WithinTx(ctx, pgx.TxOptions{}, func(q *Queries) error {
		if _, err := q.db.Exec(ctx, `
INSERT INTO planning.route (id, owner_id, city, lifecycle_state, current_revision, created_at, updated_at)
VALUES ($1, $2, 'moscow', 'draft', 1, $3, $3)`, encodeUUID([16]byte(routeID)), encodeUUID([16]byte(actorID)), now); err != nil {
			return err
		}
		_, err := q.db.Exec(ctx, `
INSERT INTO planning.route_revision (
    route_id, revision, lifecycle_state, archetype_id, timezone, start_at, end_at,
    origin, input_schema_version, constraints, catalog_revision, result_status,
    warnings, cost_summary, mutation_kind, created_at
) VALUES (
    $1, 1, 'draft', 'test', 'Europe/Moscow', $2::timestamptz, $2::timestamptz + interval '1 hour',
    ST_SetSRID(ST_MakePoint(37.6, 55.7), 4326), 1, '{}'::jsonb, 0, 'READY',
    '[]'::jsonb, '{}'::jsonb, 'create', $2
)`, encodeUUID([16]byte(routeID)), now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	makeEnvelope := func(key byte) command.Envelope {
		return command.Envelope{ActorID: actorID, Operation: command.SaveRoute, Key: [16]byte{key}, RequestHash: [32]byte{key}}
	}
	mutateRevision := func(expected domain.RouteRevisionNumber, calls *atomic.Int32) func(*Queries) (command.Result, error) {
		return func(q *Queries) (command.Result, error) {
			calls.Add(1)
			access, err := q.LockOwnedRoute(ctx, routeID, actorID)
			if err != nil {
				return command.Result{}, err
			}
			if err := RequireRevision(access, expected); err != nil {
				return command.Result{}, err
			}
			next := expected + 1
			if _, err := q.db.Exec(ctx, `
INSERT INTO planning.route_revision (
    route_id, revision, parent_revision, lifecycle_state, archetype_id, timezone,
    start_at, end_at, origin, input_schema_version, constraints, catalog_revision,
    result_status, warnings, cost_summary, mutation_kind, created_at
) SELECT route_id, $2, revision, lifecycle_state, archetype_id, timezone,
    start_at, end_at, origin, input_schema_version, constraints, catalog_revision,
    result_status, warnings, cost_summary, 'save', now()
FROM planning.route_revision WHERE route_id = $1 AND revision = $3`,
				encodeUUID([16]byte(routeID)), int64(next), int64(expected)); err != nil {
				return command.Result{}, err
			}
			if _, err := q.db.Exec(ctx, `UPDATE planning.route SET current_revision = $2, updated_at = now() WHERE id = $1`, encodeUUID([16]byte(routeID)), int64(next)); err != nil {
				return command.Result{}, err
			}
			return command.Result{HTTPStatus: 200, ResponseBody: []byte(`{"status":"READY"}`), RouteID: &routeID, ResultingRevision: &next}, nil
		}
	}
	var calls atomic.Int32
	first, err := executor.Execute(ctx, makeEnvelope(1), mutateRevision(1, &calls))
	if err != nil || first.Replayed || first.ResultingRevision == nil || *first.ResultingRevision != 2 {
		t.Fatalf("first result=%+v error=%v", first, err)
	}
	replayed, err := executor.Execute(ctx, makeEnvelope(1), func(*Queries) (command.Result, error) {
		return command.Result{}, errors.New("replay must not mutate")
	})
	if err != nil || !replayed.Replayed || calls.Load() != 1 {
		t.Fatalf("replay=%+v calls=%d error=%v", replayed, calls.Load(), err)
	}
	mismatch := makeEnvelope(1)
	mismatch.RequestHash = [32]byte{99}
	if _, err := executor.Execute(ctx, mismatch, mutateRevision(2, &calls)); !errors.Is(err, command.ErrIdempotencyKeyReused) {
		t.Fatalf("mismatch error=%v", err)
	}
	if _, err := executor.Execute(ctx, makeEnvelope(2), mutateRevision(1, &calls)); err == nil {
		t.Fatal("stale revision accepted")
	} else {
		var conflict *command.RevisionConflict
		if !errors.As(err, &conflict) || conflict.Current != 2 {
			t.Fatalf("stale revision error=%v", err)
		}
	}

	var concurrentCalls atomic.Int32
	var wg sync.WaitGroup
	type outcome struct {
		result command.Result
		err    error
	}
	outcomes := make(chan outcome, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := executor.Execute(ctx, makeEnvelope(3), func(q *Queries) (command.Result, error) {
				concurrentCalls.Add(1)
				time.Sleep(100 * time.Millisecond)
				return mutateRevision(2, &calls)(q)
			})
			outcomes <- outcome{result: result, err: err}
		}()
	}
	wg.Wait()
	close(outcomes)
	replays := 0
	for outcome := range outcomes {
		if outcome.err != nil {
			t.Fatal(outcome.err)
		}
		if outcome.result.Replayed {
			replays++
		}
	}
	if concurrentCalls.Load() != 1 || replays != 1 {
		t.Fatalf("concurrent callbacks=%d replays=%d", concurrentCalls.Load(), replays)
	}

	var oldRevisionCalls atomic.Int32
	outcomes = make(chan outcome, 2)
	for _, key := range []byte{4, 5} {
		wg.Add(1)
		go func(key byte) {
			defer wg.Done()
			result, err := executor.Execute(ctx, makeEnvelope(key), mutateRevision(3, &oldRevisionCalls))
			outcomes <- outcome{result: result, err: err}
		}(key)
	}
	wg.Wait()
	close(outcomes)
	successes, conflicts := 0, 0
	for outcome := range outcomes {
		if outcome.err == nil {
			successes++
			continue
		}
		var conflict *command.RevisionConflict
		if errors.As(outcome.err, &conflict) && conflict.Current == 4 {
			conflicts++
		} else {
			t.Fatalf("unexpected concurrent error=%v", outcome.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	var revisionCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM planning.route_revision WHERE route_id = $1`, encodeUUID([16]byte(routeID))).Scan(&revisionCount); err != nil {
		t.Fatal(err)
	}
	if revisionCount != 4 {
		t.Fatalf("revision count=%d, want 4", revisionCount)
	}
	var updatedBefore time.Time
	if err := pool.QueryRow(ctx, `SELECT updated_at FROM planning.route WHERE id = $1`, encodeUUID([16]byte(routeID))).Scan(&updatedBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(ctx, makeEnvelope(6), func(q *Queries) (command.Result, error) {
		if _, err := q.db.Exec(ctx, `UPDATE planning.route SET updated_at = updated_at + interval '1 day' WHERE id = $1`, encodeUUID([16]byte(routeID))); err != nil {
			return command.Result{}, err
		}
		return command.Result{}, errors.New("rollback")
	}); err == nil || err.Error() != "rollback" {
		t.Fatalf("rollback error=%v", err)
	}
	var updatedAfter time.Time
	if err := pool.QueryRow(ctx, `SELECT updated_at FROM planning.route WHERE id = $1`, encodeUUID([16]byte(routeID))).Scan(&updatedAfter); err != nil {
		t.Fatal(err)
	}
	if !updatedAfter.Equal(updatedBefore) {
		t.Fatal("callback error did not roll back route update")
	}
	if _, err := executor.Execute(ctx, makeEnvelope(7), func(q *Queries) (command.Result, error) {
		if _, err := q.db.Exec(ctx, `UPDATE planning.route SET current_revision = 999 WHERE id = $1`, encodeUUID([16]byte(routeID))); err != nil {
			return command.Result{}, err
		}
		return command.Result{HTTPStatus: 200, ResponseBody: []byte(`{}`)}, nil
	}); err == nil {
		t.Fatal("deferred commit failure was accepted")
	}
	var currentRevision int64
	if err := pool.QueryRow(ctx, `SELECT current_revision FROM planning.route WHERE id = $1`, encodeUUID([16]byte(routeID))).Scan(&currentRevision); err != nil {
		t.Fatal(err)
	}
	if currentRevision != 4 {
		t.Fatalf("failed commit left revision %d", currentRevision)
	}
	var resultCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gateway_ops.command_result WHERE actor_id = $1 AND operation = $2`, encodeUUID([16]byte(actorID)), command.SaveRoute).Scan(&resultCount); err != nil {
		t.Fatal(err)
	}
	if resultCount != 3 {
		t.Fatalf("stored command count=%d, want 3", resultCount)
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if _, err := executor.Execute(cancelled, makeEnvelope(8), func(*Queries) (command.Result, error) {
		return command.Result{}, errors.New("cancelled command reached mutation")
	}); err == nil || errors.Is(err, context.Canceled) == false {
		t.Fatalf("cancelled context error=%v", err)
	}
	if _, err := queries.LockOwnedRoute(ctx, routeID, domain.UserID(randomID(t))); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other owner error=%v", err)
	}
	if _, err := queries.LockOwnedRoute(ctx, domain.RouteID(randomID(t)), actorID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing route error=%v", err)
	}
}
