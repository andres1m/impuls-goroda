package postgres

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/andres1m/impuls-goroda/pkg/catalogevent"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/delivery"
)

func outboxPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("SYNCER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("SYNCER_TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// testDestination is one the running relay of a shared stand does not deliver, so committed test rows
// stay put; the tests remove them afterwards.
const testDestination = "gateway"

func insertTestRows(t *testing.T, pool *pgxpool.Pool, n int, state string, next time.Time, leaseUntil *time.Time) []uuid.UUID {
	t.Helper()
	ids := make([]uuid.UUID, n)
	for i := range ids {
		ids[i] = uuid.New()
		_, err := pool.Exec(context.Background(), `
			INSERT INTO integration.change_delivery
				(id, change_id, city, destination, event_type, payload, state, next_attempt_at, lease_until, created_at)
			VALUES ($1, $1, 'perm', $2, 'test.outbox', '{}', $3, $4, $5, $4)`,
			ids[i], testDestination, state, next, leaseUntil)
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM integration.change_delivery WHERE id = ANY($1)`, ids)
	})
	return ids
}

func rowState(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) (state string, attempts int, next time.Time) {
	t.Helper()
	err := pool.QueryRow(context.Background(), `SELECT state, attempts, next_attempt_at FROM integration.change_delivery WHERE id = $1`, id).
		Scan(&state, &attempts, &next)
	if err != nil {
		t.Fatal(err)
	}
	return state, attempts, next
}

func claimed(items []delivery.Item, ids []uuid.UUID) int {
	n := 0
	for _, it := range items {
		for _, id := range ids {
			if it.ID == id {
				n++
			}
		}
	}
	return n
}

func TestEnqueueRevisionIntegration(t *testing.T) {
	pool := outboxPool(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	at := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	m := catalogevent.Invalidation{City: "perm", CatalogRevision: 42, Reason: catalogevent.ReasonSeed, PublishedAt: at}
	if err := EnqueueRevision(ctx, tx, m); err != nil {
		t.Fatal(err)
	}
	var (
		state, destination, eventType string
		revision                      int64
		payload                       []byte
		next                          time.Time
	)
	err = tx.QueryRow(ctx, `
		SELECT state, destination, event_type, catalog_revision, payload, next_attempt_at
		FROM integration.change_delivery WHERE city = 'perm' AND catalog_revision = 42 AND event_type = $1`, catalogevent.EventType).
		Scan(&state, &destination, &eventType, &revision, &payload, &next)
	if err != nil {
		t.Fatal(err)
	}
	if state != "pending" || destination != "redis" || !next.Equal(at) {
		t.Fatalf("row %s %s %v", state, destination, next)
	}
	got, err := catalogevent.Decode(payload)
	if err != nil || got.CatalogRevision != 42 || got.City != "perm" {
		t.Fatalf("payload %s: %v", payload, err)
	}
}

func TestClaimSkipsRowsAnotherRelayHolds(t *testing.T) {
	pool := outboxPool(t)
	now := time.Now().UTC()
	ids := insertTestRows(t, pool, 6, "pending", now.Add(-time.Minute), nil)
	store := NewDeliveries(pool)
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		batches [][]delivery.Item
	)
	for range 3 {
		wg.Go(func() {
			items, err := store.Claim(context.Background(), now, now.Add(time.Minute), []string{testDestination}, 6)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			batches = append(batches, items)
			mu.Unlock()
		})
	}
	wg.Wait()
	total := 0
	for _, b := range batches {
		total += claimed(b, ids)
	}
	if total != len(ids) {
		t.Fatalf("claimed %d rows of %d across relays", total, len(ids))
	}
	state, attempts, _ := rowState(t, pool, ids[0])
	if state != "in_flight" || attempts != 1 {
		t.Fatalf("claimed row is %s after %d attempts", state, attempts)
	}
}

func TestClaimTakesExpiredLeasesAndDueRetries(t *testing.T) {
	pool := outboxPool(t)
	now := time.Now().UTC()
	expired := now.Add(-time.Second)
	held := now.Add(time.Minute)
	takeExpired := insertTestRows(t, pool, 1, "in_flight", now.Add(-time.Hour), &expired)
	keepHeld := insertTestRows(t, pool, 1, "in_flight", now.Add(-time.Hour), &held)
	takeDue := insertTestRows(t, pool, 1, "failed", now.Add(-time.Second), nil)
	keepLater := insertTestRows(t, pool, 1, "failed", now.Add(time.Hour), nil)
	keepDone := insertTestRows(t, pool, 1, "delivered", now.Add(-time.Hour), nil)

	items, err := NewDeliveries(pool).Claim(context.Background(), now, now.Add(time.Minute), []string{testDestination}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if claimed(items, takeExpired) != 1 || claimed(items, takeDue) != 1 ||
		claimed(items, keepHeld)+claimed(items, keepLater)+claimed(items, keepDone) != 0 {
		t.Fatalf("claimed %+v", items)
	}
}

func TestClaimLeavesOtherDestinations(t *testing.T) {
	pool := outboxPool(t)
	now := time.Now().UTC()
	ids := insertTestRows(t, pool, 1, "pending", now.Add(-time.Minute), nil)
	items, err := NewDeliveries(pool).Claim(context.Background(), now, now.Add(time.Minute), []string{"kafka"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if claimed(items, ids) != 0 {
		t.Fatal("claimed a row of another destination")
	}
}

func TestMarksAndBacklog(t *testing.T) {
	pool := outboxPool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	ids := insertTestRows(t, pool, 2, "pending", now.Add(-time.Minute), nil)
	store := NewDeliveries(pool)
	count, age, err := store.Backlog(ctx, now)
	if err != nil || count < 2 || age < time.Minute {
		t.Fatalf("backlog %d, oldest %v: %v", count, age, err)
	}
	items := claimOwn(t, store, ids, now, now.Add(time.Minute))
	if err := store.Delivered(ctx, items[ids[0]], now); err != nil {
		t.Fatal(err)
	}
	next := now.Add(time.Hour).Truncate(time.Microsecond)
	if err := store.Failed(ctx, items[ids[1]], next); err != nil {
		t.Fatal(err)
	}
	if state, _, _ := rowState(t, pool, ids[0]); state != "delivered" {
		t.Fatalf("delivered row is %s", state)
	}
	if state, _, at := rowState(t, pool, ids[1]); state != "failed" || !at.Equal(next) {
		t.Fatalf("failed row is %s, next at %v", state, at)
	}
}

// claimOwn claims the rows and returns the items by id; it fails if any was not claimed.
func claimOwn(t *testing.T, store *Deliveries, ids []uuid.UUID, now, leaseUntil time.Time) map[uuid.UUID]delivery.Item {
	t.Helper()
	items, err := store.Claim(context.Background(), now, leaseUntil, []string{testDestination}, 100)
	if err != nil {
		t.Fatal(err)
	}
	own := map[uuid.UUID]delivery.Item{}
	for _, it := range items {
		for _, id := range ids {
			if it.ID == id {
				own[id] = it
			}
		}
	}
	if len(own) != len(ids) {
		t.Fatalf("claimed %d of %d rows", len(own), len(ids))
	}
	return own
}

func TestMarkAfterLostLeaseIsRefused(t *testing.T) {
	pool := outboxPool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	ids := insertTestRows(t, pool, 1, "pending", now.Add(-time.Minute), nil)
	store := NewDeliveries(pool)
	first := claimOwn(t, store, ids, now, now.Add(time.Second))[ids[0]]
	later := now.Add(2 * time.Second)
	second := claimOwn(t, store, ids, later, later.Add(time.Minute))[ids[0]]
	if err := store.Delivered(ctx, first, later); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("mark under a lost lease: %v", err)
	}
	if err := store.Failed(ctx, first, later); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("mark under a lost lease: %v", err)
	}
	if state, attempts, _ := rowState(t, pool, ids[0]); state != "in_flight" || attempts != 2 {
		t.Fatalf("row is %s after %d attempts", state, attempts)
	}
	if err := store.Delivered(ctx, second, later); err != nil {
		t.Fatal(err)
	}
}
