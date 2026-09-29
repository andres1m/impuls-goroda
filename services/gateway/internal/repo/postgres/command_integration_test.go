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
	"github.com/jackc/pgx/v5/pgxpool"
)

type commandTestFixture struct {
	pool       *pgxpool.Pool
	transactor *Transactor
	executor   *CommandExecutor
	queries    *Queries
	actorID    domain.UserID
	routeID    domain.RouteID
}

func (f *commandTestFixture) envelope(key byte) *command.Envelope {
	return &command.Envelope{
		ActorID:     f.actorID,
		Operation:   command.SaveRoute,
		Key:         [16]byte{key},
		RequestHash: [32]byte{key},
	}
}

func (f *commandTestFixture) mutateRevision(
	ctx context.Context,
	expected domain.RouteRevisionNumber,
	calls *atomic.Int32,
) func(*Queries) (command.Result, error) {
	return func(q *Queries) (command.Result, error) {
		calls.Add(1)
		access, lockErr := q.LockOwnedRoute(ctx, f.routeID, f.actorID)
		if lockErr != nil {
			return command.Result{}, lockErr
		}
		if revErr := RequireRevision(access, expected); revErr != nil {
			return command.Result{}, revErr
		}
		next := expected + 1
		if _, execErr := q.db.Exec(ctx, `
INSERT INTO planning.route_revision (
    route_id, revision, parent_revision, lifecycle_state, archetype_id, timezone,
    start_at, end_at, origin, input_schema_version, constraints, catalog_revision,
    result_status, warnings, cost_summary, mutation_kind, created_at
) SELECT route_id, $2, revision, lifecycle_state, archetype_id, timezone,
    start_at, end_at, origin, input_schema_version, constraints, catalog_revision,
    result_status, warnings, cost_summary, 'save', now()
FROM planning.route_revision WHERE route_id = $1 AND revision = $3`,
			encodeUUID([16]byte(f.routeID)), int64(next), int64(expected)); execErr != nil {
			return command.Result{}, execErr
		}
		if _, execErr := q.db.Exec(
			ctx,
			`UPDATE planning.route SET current_revision = $2, updated_at = now() WHERE id = $1`,
			encodeUUID([16]byte(f.routeID)),
			int64(next),
		); execErr != nil {
			return command.Result{}, execErr
		}
		routeID := f.routeID
		return command.Result{
			HTTPStatus:        200,
			ResponseBody:      []byte(`{"status":"READY"}`),
			RouteID:           &routeID,
			ResultingRevision: &next,
		}, nil
	}
}

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

	fixture := setupCommandTestFixture(ctx, t, pool)
	var calls atomic.Int32
	assertReplayAndConflict(ctx, t, fixture, &calls)
	assertConcurrentDeduplication(ctx, t, fixture, &calls)
	assertConcurrentRevisionConflict(ctx, t, fixture)
	assertRollbackAndCancellation(ctx, t, fixture)
}

func setupCommandTestFixture(ctx context.Context, t *testing.T, pool *pgxpool.Pool) *commandTestFixture {
	t.Helper()
	transactor, err := NewTransactor(pool)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := NewCommandExecutor(transactor)
	if err != nil {
		t.Fatal(err)
	}
	queries, err := NewQueries(pool)
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
	if _, upsertErr := queries.UpsertTestAccount(ctx, &account); upsertErr != nil {
		t.Fatal(upsertErr)
	}
	if txErr := transactor.WithinTx(ctx, nil, func(q *Queries) error {
		if _, execErr := q.db.Exec(ctx, `
INSERT INTO planning.route (id, owner_id, city, lifecycle_state, current_revision, created_at, updated_at)
VALUES ($1, $2, 'moscow', 'draft', 1, $3, $3)`,
			encodeUUID([16]byte(routeID)), encodeUUID([16]byte(actorID)), now); execErr != nil {
			return execErr
		}
		_, execErr := q.db.Exec(ctx, `
INSERT INTO planning.route_revision (
    route_id, revision, lifecycle_state, archetype_id, timezone, start_at, end_at,
    origin, input_schema_version, constraints, catalog_revision, result_status,
    warnings, cost_summary, mutation_kind, created_at
) VALUES (
    $1, 1, 'draft', 'test', 'Europe/Moscow', $2::timestamptz, $2::timestamptz + interval '1 hour',
    ST_SetSRID(ST_MakePoint(37.6, 55.7), 4326), 1, '{}'::jsonb, 0, 'READY',
    '[]'::jsonb, '{}'::jsonb, 'create', $2
)`, encodeUUID([16]byte(routeID)), now)
		return execErr
	}); txErr != nil {
		t.Fatal(txErr)
	}
	return &commandTestFixture{
		pool:       pool,
		transactor: transactor,
		executor:   executor,
		queries:    queries,
		actorID:    actorID,
		routeID:    routeID,
	}
}

func assertReplayAndConflict(
	ctx context.Context,
	t *testing.T,
	f *commandTestFixture,
	calls *atomic.Int32,
) {
	t.Helper()
	first, err := f.executor.Execute(ctx, f.envelope(1), f.mutateRevision(ctx, 1, calls))
	if err != nil || first.Replayed || first.ResultingRevision == nil || *first.ResultingRevision != 2 {
		t.Fatalf("first result=%+v error=%v", first, err)
	}
	replayed, err := f.executor.Execute(ctx, f.envelope(1), func(*Queries) (command.Result, error) {
		return command.Result{}, errors.New("replay must not mutate")
	})
	if err != nil || !replayed.Replayed || calls.Load() != 1 {
		t.Fatalf("replay=%+v calls=%d error=%v", replayed, calls.Load(), err)
	}
	mismatch := f.envelope(1)
	mismatch.RequestHash = [32]byte{99}
	if _, execErr := f.executor.Execute(
		ctx,
		mismatch,
		f.mutateRevision(ctx, 2, calls),
	); !errors.Is(execErr, command.ErrIdempotencyKeyReused) {
		t.Fatalf("mismatch error=%v", execErr)
	}
	_, staleErr := f.executor.Execute(ctx, f.envelope(2), f.mutateRevision(ctx, 1, calls))
	if staleErr == nil {
		t.Fatal("stale revision accepted")
	}
	var conflict *command.RevisionConflictError
	if !errors.As(staleErr, &conflict) || conflict.Current != 2 {
		t.Fatalf("stale revision error=%v", staleErr)
	}
}

type commandOutcome struct {
	result command.Result
	err    error
}

func assertConcurrentDeduplication(
	ctx context.Context,
	t *testing.T,
	f *commandTestFixture,
	calls *atomic.Int32,
) {
	t.Helper()
	var concurrentCalls atomic.Int32
	var wg sync.WaitGroup
	outcomes := make(chan commandOutcome, 2)
	for range 2 {
		wg.Go(func() {
			result, err := f.executor.Execute(ctx, f.envelope(3), func(q *Queries) (command.Result, error) {
				concurrentCalls.Add(1)
				time.Sleep(100 * time.Millisecond)
				return f.mutateRevision(ctx, 2, calls)(q)
			})
			outcomes <- commandOutcome{result: result, err: err}
		})
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
}

func assertConcurrentRevisionConflict(
	ctx context.Context,
	t *testing.T,
	f *commandTestFixture,
) {
	t.Helper()
	var oldRevisionCalls atomic.Int32
	var wg sync.WaitGroup
	outcomes := make(chan commandOutcome, 2)
	for _, key := range []byte{4, 5} {
		wg.Add(1)
		go func(key byte) {
			defer wg.Done()
			result, err := f.executor.Execute(ctx, f.envelope(key), f.mutateRevision(ctx, 3, &oldRevisionCalls))
			outcomes <- commandOutcome{result: result, err: err}
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
		var conflict *command.RevisionConflictError
		if errors.As(outcome.err, &conflict) && conflict.Current == 4 {
			conflicts++
		} else {
			t.Fatalf("unexpected concurrent error=%v", outcome.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}

func assertRollbackAndCancellation(
	ctx context.Context,
	t *testing.T,
	f *commandTestFixture,
) {
	t.Helper()
	assertRevisionAndRollback(ctx, t, f)
	assertDeferredCommitAndAccess(ctx, t, f)
}

func assertRevisionAndRollback(
	ctx context.Context,
	t *testing.T,
	f *commandTestFixture,
) {
	t.Helper()
	routeUUID := encodeUUID([16]byte(f.routeID))
	var revisionCount int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM planning.route_revision WHERE route_id = $1`, routeUUID).
		Scan(&revisionCount); err != nil {
		t.Fatal(err)
	}
	if revisionCount != 4 {
		t.Fatalf("revision count=%d, want 4", revisionCount)
	}
	var updatedBefore time.Time
	if err := f.pool.QueryRow(ctx, `SELECT updated_at FROM planning.route WHERE id = $1`, routeUUID).
		Scan(&updatedBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := f.executor.Execute(ctx, f.envelope(6), func(q *Queries) (command.Result, error) {
		if _, execErr := q.db.Exec(
			ctx,
			`UPDATE planning.route SET updated_at = updated_at + interval '1 day' WHERE id = $1`,
			routeUUID,
		); execErr != nil {
			return command.Result{}, execErr
		}
		return command.Result{}, errors.New("rollback")
	}); err == nil || err.Error() != "rollback" {
		t.Fatalf("rollback error=%v", err)
	}
	var updatedAfter time.Time
	if err := f.pool.QueryRow(ctx, `SELECT updated_at FROM planning.route WHERE id = $1`, routeUUID).
		Scan(&updatedAfter); err != nil {
		t.Fatal(err)
	}
	if !updatedAfter.Equal(updatedBefore) {
		t.Fatal("callback error did not roll back route update")
	}
}

func assertDeferredCommitAndAccess(
	ctx context.Context,
	t *testing.T,
	f *commandTestFixture,
) {
	t.Helper()
	routeUUID := encodeUUID([16]byte(f.routeID))
	if _, err := f.executor.Execute(ctx, f.envelope(7), func(q *Queries) (command.Result, error) {
		if _, execErr := q.db.Exec(
			ctx,
			`UPDATE planning.route SET current_revision = 999 WHERE id = $1`,
			routeUUID,
		); execErr != nil {
			return command.Result{}, execErr
		}
		return command.Result{HTTPStatus: 200, ResponseBody: []byte(`{}`)}, nil
	}); err == nil {
		t.Fatal("deferred commit failure was accepted")
	}
	var currentRevision int64
	if err := f.pool.QueryRow(ctx, `SELECT current_revision FROM planning.route WHERE id = $1`, routeUUID).
		Scan(&currentRevision); err != nil {
		t.Fatal(err)
	}
	if currentRevision != 4 {
		t.Fatalf("failed commit left revision %d", currentRevision)
	}
	var resultCount int
	if err := f.pool.QueryRow(
		ctx,
		`SELECT count(*) FROM gateway_ops.command_result WHERE actor_id = $1 AND operation = $2`,
		encodeUUID([16]byte(f.actorID)),
		command.SaveRoute,
	).Scan(&resultCount); err != nil {
		t.Fatal(err)
	}
	if resultCount != 3 {
		t.Fatalf("stored command count=%d, want 3", resultCount)
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if _, err := f.executor.Execute(cancelled, f.envelope(8), func(*Queries) (command.Result, error) {
		return command.Result{}, errors.New("cancelled command reached mutation")
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context error=%v", err)
	}
	if _, err := f.queries.LockOwnedRoute(ctx, f.routeID, domain.UserID(randomID(t))); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other owner error=%v", err)
	}
	if _, err := f.queries.LockOwnedRoute(ctx, domain.RouteID(randomID(t)), f.actorID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing route error=%v", err)
	}
}
