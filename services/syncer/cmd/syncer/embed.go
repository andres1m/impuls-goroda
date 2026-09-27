package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/andres1m/impuls-goroda/pkg/ai"
	"github.com/andres1m/impuls-goroda/pkg/config"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/embedding"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/repo/postgres"
)

// Hosted embedding APIs cap the inputs of one request; 2048 is the common ceiling.
const maxEmbedBatch = 2048

func parseEmbedArgs(args []string) ([]domain.City, int, error) {
	usage := errors.New("usage: syncer embed [--city moscow|perm] [--batch 1..2048]")
	flags := flag.NewFlagSet("embed", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	city := flags.String("city", "", "")
	batch := flags.Int("batch", 64, "")
	if err := flags.Parse(args); err != nil {
		return nil, 0, errors.Join(err, usage)
	}
	if flags.NArg() != 0 || *batch < 1 || *batch > maxEmbedBatch {
		return nil, 0, usage
	}
	if *city == "" {
		return []domain.City{domain.Moscow, domain.Perm}, *batch, nil
	}
	parsed, err := domain.ParseCity(*city)
	if err != nil {
		return nil, 0, errors.Join(err, usage)
	}
	return []domain.City{parsed}, *batch, nil
}

func runEmbed(ctx context.Context, args []string) error {
	cities, batch, err := parseEmbedArgs(args)
	if err != nil {
		return err
	}
	var cfg appConfig
	if err := config.Load(configPath, &cfg); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	models, err := ai.New(cfg.AI)
	if err != nil {
		return fmt.Errorf("ai config: %w", err)
	}
	database, err := openDatabase(ctx)
	if err != nil {
		return err
	}
	defer database.Stop(context.Background())

	embedder := models.Embedder()
	for _, city := range cities {
		result, err := embedCity(ctx, database.Pool, embedder, city, batch)
		if err != nil {
			return fmt.Errorf("embed %s: %w", city, err)
		}
		space := embedder.Space()
		fmt.Printf("%s: entities %d, embedded %d, space %s %s\n", city, result.Entities, result.Embedded, space.Key, space.Version)
	}
	return nil
}

type embedResult struct {
	Entities, Embedded int
}

// embedCity writes each vector as soon as its batch returns, so an interrupted run resumes
// where it stopped.
func embedCity(ctx context.Context, db postgres.EmbeddingDB, embedder ai.Embedder, city domain.City, batch int) (embedResult, error) {
	space := embedder.Space()
	entities, err := postgres.EmbeddingEntities(ctx, db, string(city), space)
	if err != nil {
		return embedResult{}, err
	}
	stale := embedding.Stale(entities)
	result := embedResult{Entities: len(entities)}
	for start := 0; start < len(stale); start += batch {
		chunk := stale[start:min(start+batch, len(stale))]
		texts := make([]string, len(chunk))
		for i, p := range chunk {
			texts[i] = p.Text
		}
		got, err := embedder.Embed(ctx, texts)
		if err != nil {
			return result, err
		}
		if got.Space != space || len(got.Vectors) != len(chunk) {
			return result, fmt.Errorf("embedder answered %d vectors in %s %s for %d texts in %s %s",
				len(got.Vectors), got.Space.Key, got.Space.Version, len(chunk), space.Key, space.Version)
		}
		for i, p := range chunk {
			if err := postgres.UpsertEmbedding(ctx, db, space, p, got.Vectors[i]); err != nil {
				return result, err
			}
			result.Embedded++
		}
	}
	return result, nil
}
