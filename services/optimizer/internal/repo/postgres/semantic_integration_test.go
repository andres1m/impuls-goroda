package postgres

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/andres1m/impuls-goroda/pkg/ai"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func axis(i int) []float32 {
	v := make([]float32, 384)
	v[i] = 1
	return v
}

func insertEmbedding(ctx context.Context, t *testing.T, tx pgx.Tx, city string, place domain.PlaceID, space ai.Space, vector []float32) {
	t.Helper()
	_, err := tx.Exec(ctx, `
		INSERT INTO catalog.entity_embedding (id, city, place_id, model_key, model_version, embedding, content_hash, created_at)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5::vector, '\x00', now())`,
		city, place, space.Key, space.Version, vectorLiteral(vector))
	if err != nil {
		t.Fatal(err)
	}
}

func TestNearestEntitiesIntegration(t *testing.T) {
	ctx, tx := fixtureTx(t)
	perm := domain.Coordinate{Longitude: 56.25, Latitude: 58.0}
	moscow := domain.Coordinate{Longitude: 37.62, Latitude: 55.75}
	space := ai.Space{Key: "test/nearest-" + t.Name(), Version: "d384"}
	other := ai.Space{Key: space.Key, Version: "d385"}

	near := insertPOI(ctx, t, tx, "perm", perm, 10, 0, true)
	far := insertPOI(ctx, t, tx, "perm", perm, 20, 0, true)
	elsewhere := insertPOI(ctx, t, tx, "moscow", moscow, 10, 0, true)
	otherModel := insertPOI(ctx, t, tx, "perm", perm, 30, 0, true)
	insertEmbedding(ctx, t, tx, "perm", near, space, axis(0))
	insertEmbedding(ctx, t, tx, "perm", far, space, axis(1))
	insertEmbedding(ctx, t, tx, "moscow", elsewhere, space, axis(0))
	insertEmbedding(ctx, t, tx, "perm", otherModel, other, axis(0))

	query := axis(0)
	query[1] = 0.1
	got, err := NewCatalog(tx).NearestEntities(ctx, "perm", space, query, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Place == nil || *got[0].Place != near || got[1].Place == nil || *got[1].Place != far {
		t.Fatalf("matches %+v", got)
	}

	limited, err := NewCatalog(tx).NearestEntities(ctx, "perm", space, query, 1)
	if err != nil || len(limited) != 1 || *limited[0].Place != near {
		t.Fatalf("limited %+v, %v", limited, err)
	}

	none, err := NewCatalog(tx).NearestEntities(ctx, "perm", ai.Space{Key: "test/absent", Version: "d384"}, query, 10)
	if err != nil || len(none) != 0 {
		t.Fatalf("absent space %+v, %v", none, err)
	}
}
