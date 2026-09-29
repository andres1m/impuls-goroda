package main

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/andres1m/impuls-goroda/pkg/ai"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/repo/postgres"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/seed"
)

func TestParseEmbedArgs(t *testing.T) {
	for _, c := range []struct {
		args   []string
		cities []domain.City
		batch  int
	}{
		{nil, []domain.City{domain.Moscow, domain.Perm}, 64},
		{[]string{"--city", "perm"}, []domain.City{domain.Perm}, 64},
		{[]string{"--batch=8"}, []domain.City{domain.Moscow, domain.Perm}, 8},
	} {
		cities, batch, err := parseEmbedArgs(c.args)
		if err != nil || !slices.Equal(cities, c.cities) || batch != c.batch {
			t.Fatalf("args %q: %v %d %v", c.args, cities, batch, err)
		}
	}
	for _, args := range [][]string{{"--city", "kazan"}, {"--batch", "0"}, {"--batch", "2049"}, {"perm"}, {"--city"}} {
		if _, _, err := parseEmbedArgs(args); err == nil {
			t.Fatalf("args %q accepted", args)
		}
	}
}

type fakeEmbedder struct {
	space ai.Space
	// answerSpace differs from space to imitate a model switched during the run.
	answerSpace ai.Space
	err         error
	batches     []int
}

func (f *fakeEmbedder) Space() ai.Space { return f.space }

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) (ai.Embedding, error) {
	f.batches = append(f.batches, len(texts))
	if f.err != nil {
		return ai.Embedding{}, f.err
	}
	out := ai.Embedding{Space: f.answerSpace, Vectors: make([][]float32, len(texts))}
	for i := range out.Vectors {
		out.Vectors[i] = make([]float32, 384)
		out.Vectors[i][0] = 1
	}
	return out, nil
}

func TestEmbedCityIntegration(t *testing.T) {
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
	ref, err := postgres.LoadSeedReference(ctx, tx)
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
	if _, seedErr := postgres.ApplySeed(ctx, tx, &rows, now); seedErr != nil {
		t.Fatal(seedErr)
	}

	space := ai.Space{Key: "test/" + t.Name(), Version: "d384"}
	embedder := &fakeEmbedder{space: space, answerSpace: space}
	first, err := embedCity(ctx, tx, embedder, domain.Perm, 5)
	if err != nil {
		t.Fatal(err)
	}
	if first.Embedded == 0 || first.Embedded != first.Entities || slices.Max(embedder.batches) > 5 {
		t.Fatalf("first run %+v, batches %v", first, embedder.batches)
	}
	second, err := embedCity(ctx, tx, embedder, domain.Perm, 5)
	if err != nil || second.Embedded != 0 || second.Entities != first.Entities {
		t.Fatalf("second run %+v, %v", second, err)
	}

	other := ai.Space{Key: "test/other", Version: "d384"}
	switched := &fakeEmbedder{space: other, answerSpace: space}
	if _, err := embedCity(ctx, tx, switched, domain.Perm, 5); err == nil {
		t.Fatal("vectors of another space were accepted")
	}
	failing := &fakeEmbedder{space: other, answerSpace: other, err: ai.ErrNotConfigured}
	if _, err := embedCity(ctx, tx, failing, domain.Perm, 5); !errors.Is(err, ai.ErrNotConfigured) {
		t.Fatalf("error %v", err)
	}
}
