package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLandingIntegration(t *testing.T) {
	databaseURL := os.Getenv("SYNCER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("SYNCER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	landing := NewLanding(pool)
	source := domain.Source{
		Key:           domain.SourceKey("test_" + randomSuffix(t)),
		Name:          "Integration test source",
		AccessMode:    domain.AccessAPI,
		SchemaVersion: "1",
		DataMode:      domain.Live,
	}
	sourceID, err := landing.EnsureSource(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { deleteSource(t, pool, sourceID) })

	again, err := landing.EnsureSource(ctx, source)
	if err != nil || again != sourceID {
		t.Fatalf("EnsureSource is not idempotent: %x, %v", again, err)
	}

	cursor, err := landing.Cursor(ctx, sourceID, domain.Perm)
	if err != nil || cursor != nil {
		t.Fatalf("fresh cursor = %s, %v", cursor, err)
	}

	first := time.Now().UTC().Truncate(time.Microsecond)
	updatedAt := first.Add(-time.Hour)
	record := domain.RawRecord{
		ExternalID:      "event:1",
		SourceURL:       "https://example.test/events/1",
		Payload:         []byte(`{"id": 1}`),
		ContentType:     "application/json",
		SourceUpdatedAt: &updatedAt,
	}

	saveExpect(ctx, t, landing, sourceID, record, first, true)
	saveExpect(ctx, t, landing, sourceID, record, first.Add(time.Minute), false)
	record.Payload = []byte(`{"id": 1, "title": "changed"}`)
	last := first.Add(2 * time.Minute)
	saveExpect(ctx, t, landing, sourceID, record, last, true)

	var rawCount int
	var lastSeen time.Time
	var state, mode string
	err = pool.QueryRow(ctx, `
		SELECT count(ri.id), sr.last_seen_at, min(ri.processing_state), min(ri.data_mode)
		FROM integration.source_record sr
		JOIN integration.raw_ingest ri ON ri.source_record_id = sr.id
		WHERE sr.source_id = $1 AND sr.external_id = 'event:1'
		GROUP BY sr.last_seen_at`, uuidParam(sourceID)).Scan(&rawCount, &lastSeen, &state, &mode)
	if err != nil {
		t.Fatal(err)
	}
	if rawCount != 2 || !lastSeen.Equal(last) || state != "pending" || mode != "live" {
		t.Fatalf("raw rows = %d, last_seen_at = %s, state = %s, mode = %s", rawCount, lastSeen, state, mode)
	}

	success := ingest.Run{SourceID: sourceID, City: domain.Perm, AttemptAt: last, Cursor: json.RawMessage(`{"snapshot_at":"2026-09-27T10:00:00Z"}`)}
	if err := landing.FinishRun(ctx, success); err != nil {
		t.Fatal(err)
	}
	failure := ingest.Run{SourceID: sourceID, City: domain.Perm, AttemptAt: last.Add(time.Hour), ErrorCode: "http_status_503"}
	if err := landing.FinishRun(ctx, failure); err != nil {
		t.Fatal(err)
	}

	cursor, err = landing.Cursor(ctx, sourceID, domain.Perm)
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]string
	if err := json.Unmarshal(cursor, &stored); err != nil || stored["snapshot_at"] != "2026-09-27T10:00:00Z" {
		t.Fatalf("cursor after failed run = %s, %v", cursor, err)
	}
	var lastAttempt, lastSuccess time.Time
	var errorCode string
	err = pool.QueryRow(ctx, `
		SELECT last_attempt_at, last_success_at, last_error_code
		FROM integration.sync_cursor WHERE source_id = $1 AND city = 'perm'`, uuidParam(sourceID)).
		Scan(&lastAttempt, &lastSuccess, &errorCode)
	if err != nil {
		t.Fatal(err)
	}
	if !lastAttempt.Equal(failure.AttemptAt) || !lastSuccess.Equal(success.AttemptAt) || errorCode != "http_status_503" {
		t.Fatalf("sync_cursor = %s, %s, %q", lastAttempt, lastSuccess, errorCode)
	}
}

func saveExpect(ctx context.Context, t *testing.T, landing *Landing, sourceID ingest.SourceID, record domain.RawRecord, at time.Time, want bool) {
	t.Helper()
	inserted, err := landing.SaveRecord(ctx, sourceID, domain.Perm, domain.Live, record, at)
	if err != nil {
		t.Fatal(err)
	}
	if inserted != want {
		t.Fatalf("SaveRecord at %s inserted = %v, want %v", at, inserted, want)
	}
}

func deleteSource(t *testing.T, pool *pgxpool.Pool, sourceID ingest.SourceID) {
	ctx := context.Background()
	for _, query := range []string{
		`DELETE FROM integration.raw_ingest WHERE source_record_id IN (SELECT id FROM integration.source_record WHERE source_id = $1)`,
		`DELETE FROM integration.source_record WHERE source_id = $1`,
		`DELETE FROM integration.sync_cursor WHERE source_id = $1`,
		`DELETE FROM integration.source WHERE id = $1`,
	} {
		if _, err := pool.Exec(ctx, query, uuidParam(sourceID)); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	}
}

func randomSuffix(t *testing.T) string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
