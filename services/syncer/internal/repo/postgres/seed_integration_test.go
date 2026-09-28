package postgres

import (
	"context"
	"maps"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/andres1m/impuls-goroda/pkg/catalogevent"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/seed"
)

func TestApplySeedIntegration(t *testing.T) {
	databaseURL := os.Getenv("SYNCER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("SYNCER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	ref, err := LoadSeedReference(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	ds, err := seed.Load(domain.Perm)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	rows, err := seed.Expand(ds, ref, "2026-10-01", now)
	if err != nil {
		t.Fatal(err)
	}

	revision := cityRevision(ctx, t, tx)
	first, err := ApplySeed(ctx, tx, rows, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.CatalogRevision != revision+1 || first.Sessions != len(rows.Sessions) || first.Prices != len(rows.Prices) {
		t.Fatalf("first apply = %+v, revision before %d", first, revision)
	}
	checkAnnounced(ctx, t, tx, first.CatalogRevision, now)
	var synthetic int
	err = tx.QueryRow(ctx, `
		SELECT count(*) FROM catalog.session s
		JOIN integration.source_record r ON r.id = s.card_source_record_id
		WHERE s.city = 'perm' AND s.id = ANY ($1::uuid[]) AND s.data_mode = 'synthetic'
			AND r.data_mode = 'synthetic' AND r.source_url = 'synthetic:' || r.external_id`,
		sessionIDs(rows)).Scan(&synthetic)
	if err != nil || synthetic != len(rows.Sessions) {
		t.Fatalf("synthetic sessions with provenance = %d of %d, %v", synthetic, len(rows.Sessions), err)
	}

	before := sessionStates(ctx, t, tx, rows)
	second, err := ApplySeed(ctx, tx, rows, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if second.CatalogRevision != first.CatalogRevision+1 {
		t.Fatalf("second revision = %d, want %d", second.CatalogRevision, first.CatalogRevision+1)
	}
	if after := sessionStates(ctx, t, tx, rows); !maps.Equal(before, after) {
		t.Fatal("reapplying the same dataset changed session versions or timestamps")
	}
	checkProjection(ctx, t, tx, rows, second.CatalogRevision)

	removed := rows.Events[0]
	ds.Events = ds.Events[1:]
	shrunk, err := seed.Expand(ds, ref, "2026-10-01", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplySeed(ctx, tx, shrunk, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var active bool
	if err := tx.QueryRow(ctx, `SELECT is_active FROM catalog.event WHERE city = 'perm' AND id = $1`, removed.ID.String()).Scan(&active); err != nil || active {
		t.Fatalf("event removed from the dataset is active = %v, %v", active, err)
	}
}

// checkAnnounced finds exactly one pending announcement of the revision for the caches.
func checkAnnounced(ctx context.Context, t *testing.T, tx pgx.Tx, revision int64, at time.Time) {
	t.Helper()
	rows, err := tx.Query(ctx, `
		SELECT payload FROM integration.change_delivery
		WHERE city = 'perm' AND catalog_revision = $1 AND destination = 'redis' AND event_type = 'catalog.revision' AND state = 'pending'`,
		revision)
	if err != nil {
		t.Fatal(err)
	}
	payloads, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	if err != nil || len(payloads) != 1 {
		t.Fatalf("announcements of revision %d: %d, %v", revision, len(payloads), err)
	}
	m, err := catalogevent.Decode(payloads[0])
	if err != nil || m.Reason != catalogevent.ReasonSeed || !m.PublishedAt.Equal(at) {
		t.Fatalf("announcement %s: %v", payloads[0], err)
	}
}

func cityRevision(ctx context.Context, t *testing.T, tx pgx.Tx) int64 {
	t.Helper()
	var revision int64
	if err := tx.QueryRow(ctx, `SELECT catalog_revision FROM ref.city WHERE code = 'perm'`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	return revision
}

func sessionIDs(rows seed.Rows) []string {
	ids := make([]string, len(rows.Sessions))
	for i, s := range rows.Sessions {
		ids[i] = s.ID.String()
	}
	return ids
}

type sessionState struct{ version, updatedAt int64 }

func sessionStates(ctx context.Context, t *testing.T, tx pgx.Tx, rows seed.Rows) map[string]sessionState {
	t.Helper()
	result, err := tx.Query(ctx, `
		SELECT id::text, version, updated_at FROM catalog.session WHERE city = 'perm' AND id = ANY ($1::uuid[])`,
		sessionIDs(rows))
	if err != nil {
		t.Fatal(err)
	}
	states := make(map[string]sessionState)
	var id string
	var version int64
	var updatedAt time.Time
	_, err = pgx.ForEachRow(result, []any{&id, &version, &updatedAt}, func() error {
		states[id] = sessionState{version, updatedAt.UnixMicro()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return states
}

func checkProjection(ctx context.Context, t *testing.T, tx pgx.Tx, rows seed.Rows, revision int64) {
	t.Helper()
	eventMasks := make(map[string]int64)
	for _, e := range rows.Events {
		eventMasks[e.PlaceID.String()] |= e.TagMask
	}
	for _, p := range rows.Places {
		var categories []string
		var mask, res8, res11, projected int64
		err := tx.QueryRow(ctx, `
			SELECT categories, tag_mask::bigint, h3_res8, h3_res11, catalog_revision
			FROM catalog.leisure_poi WHERE city = 'perm' AND id = $1 AND is_active AND data_mode = 'synthetic'`,
			p.ID.String()).Scan(&categories, &mask, &res8, &res11, &projected)
		if err != nil {
			t.Fatalf("projection of %s: %v", p.ExternalID, err)
		}
		if len(categories) == 0 || mask != p.TagMask|eventMasks[p.ID.String()] ||
			res8 != p.H3Res8 || res11 != p.H3Res11 || projected != revision {
			t.Fatalf("projection of %s: categories %v, mask %b, h3 %d/%d, revision %d", p.ExternalID, categories, mask, res8, res11, projected)
		}
	}
}
