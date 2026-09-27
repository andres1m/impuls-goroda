package postgres

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

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
	if _, err := ApplySeed(ctx, tx, rows, now); err != nil {
		t.Fatal(err)
	}

	space := ai.Space{Key: "test/" + t.Name(), Version: "d384"}
	entities, err := EmbeddingEntities(ctx, tx, "perm", space)
	if err != nil {
		t.Fatal(err)
	}
	var places, events int
	for _, e := range entities {
		if e.StoredHash != nil || e.City != "perm" || e.Title == "" {
			t.Fatalf("entity %+v", e)
		}
		switch {
		case e.Place != nil && e.Event == nil:
			places++
		case e.Event != nil && e.Place == nil:
			events++
			if e.Category == "" {
				t.Fatalf("event without category title %+v", e)
			}
		default:
			t.Fatalf("entity must be a place or an event: %+v", e)
		}
	}
	if places < len(rows.Places) || events < len(rows.Events) {
		t.Fatalf("places %d events %d, seeded %d and %d", places, events, len(rows.Places), len(rows.Events))
	}
	if !slicesContainInterests(entities) {
		t.Fatal("no entity carries interest titles")
	}

	prepared := embedding.Stale(entities)[:2]
	for _, p := range prepared {
		if err := UpsertEmbedding(ctx, tx, space, p, vector384(1)); err != nil {
			t.Fatal(err)
		}
	}
	changed := prepared[0]
	changed.Hash = embedding.Hash("other text")
	if err := UpsertEmbedding(ctx, tx, space, changed, vector384(2)); err != nil {
		t.Fatal(err)
	}

	again, err := EmbeddingEntities(ctx, tx, "perm", space)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != len(entities) {
		t.Fatalf("entities %d, then %d", len(entities), len(again))
	}
	stored := 0
	for _, e := range again {
		if e.StoredHash != nil {
			stored++
		}
	}
	if stored != 2 {
		t.Fatalf("stored hashes %d", stored)
	}
	if left := embedding.Stale(again); len(left) != len(entities)-1 {
		t.Fatalf("stale after upsert %d of %d", len(left), len(entities))
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
	if !bytes.Equal(prepared[1].Hash, embedding.Hash(prepared[1].Text)) {
		t.Fatal("prepared hash mismatch")
	}
}

func slicesContainInterests(entities []embedding.Entity) bool {
	for _, e := range entities {
		if len(e.Interests) > 0 {
			return true
		}
	}
	return false
}
