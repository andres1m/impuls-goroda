package postgres

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/andres1m/impuls-goroda/pkg/ai"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/embedding"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/seed"
)

func vector384(first float32) []float32 {
	v := make([]float32, 384)
	v[0] = first
	return v
}

func TestEmbeddingsIntegration(t *testing.T) {
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

	rows := seedPermForEmbeddings(ctx, t, tx)

	space := ai.Space{Key: "test/" + t.Name(), Version: "d384"}
	entities, err := EmbeddingEntities(ctx, tx, "perm", space)
	if err != nil {
		t.Fatal(err)
	}
	checkInitialEmbeddingEntities(t, entities, len(rows.Places), len(rows.Events))

	prepared := embedding.Stale(entities)[:2]
	for i := range prepared {
		if upsertErr := UpsertEmbedding(ctx, tx, space, &prepared[i], vector384(1)); upsertErr != nil {
			t.Fatal(upsertErr)
		}
	}
	changed := prepared[0]
	changed.Hash = embedding.Hash("other text")
	if upsertErr := UpsertEmbedding(ctx, tx, space, &changed, vector384(2)); upsertErr != nil {
		t.Fatal(upsertErr)
	}

	checkUpsertedEmbeddings(ctx, t, tx, space, len(entities))
	if !bytes.Equal(prepared[1].Hash, embedding.Hash(prepared[1].Text)) {
		t.Fatal("prepared hash mismatch")
	}
}

func seedPermForEmbeddings(ctx context.Context, t *testing.T, tx pgx.Tx) seed.Rows {
	t.Helper()
	ref, err := LoadSeedReference(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	ds, err := seed.Load(domain.Perm)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	rows, err := seed.Expand(&ds, ref, "2026-10-01", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ApplySeed(ctx, tx, &rows, now); err != nil {
		t.Fatal(err)
	}
	return rows
}

func checkInitialEmbeddingEntities(t *testing.T, entities []embedding.Entity, minPlaces, minEvents int) {
	t.Helper()
	var places, events int
	for i := range entities {
		e := &entities[i]
		if e.StoredHash != nil || e.City != "perm" || e.Title == "" {
			t.Fatalf("entity %+v", *e)
		}
		switch {
		case e.Place != nil && e.Event == nil:
			places++
		case e.Event != nil && e.Place == nil:
			events++
			if e.Category == "" {
				t.Fatalf("event without category title %+v", *e)
			}
		default:
			t.Fatalf("entity must be a place or an event: %+v", *e)
		}
	}
	if places < minPlaces || events < minEvents {
		t.Fatalf("places %d events %d, seeded %d and %d", places, events, minPlaces, minEvents)
	}
	if !slicesContainInterests(entities) {
		t.Fatal("no entity carries interest titles")
	}
}

func checkUpsertedEmbeddings(ctx context.Context, t *testing.T, tx pgx.Tx, space ai.Space, total int) {
	t.Helper()
	again, err := EmbeddingEntities(ctx, tx, "perm", space)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != total {
		t.Fatalf("entities %d, then %d", total, len(again))
	}
	stored := 0
	for i := range again {
		if again[i].StoredHash != nil {
			stored++
		}
	}
	if stored != 2 {
		t.Fatalf("stored hashes %d", stored)
	}
	if left := embedding.Stale(again); len(left) != total-1 {
		t.Fatalf("stale after upsert %d of %d", len(left), total)
	}

	var count int
	var first float32
	err = tx.QueryRow(ctx, `
		SELECT count(*), max((embedding::real[])[1]) FROM catalog.entity_embedding
		WHERE model_key = $1 AND model_version = $2`, space.Key, space.Version).Scan(&count, &first)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || first != 2 {
		t.Fatalf("rows %d, updated vector starts with %v", count, first)
	}
}

func slicesContainInterests(entities []embedding.Entity) bool {
	for i := range entities {
		if len(entities[i].Interests) > 0 {
			return true
		}
	}
	return false
}
