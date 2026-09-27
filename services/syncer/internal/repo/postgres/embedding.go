package postgres

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/andres1m/impuls-goroda/pkg/ai"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/embedding"
)

type EmbeddingDB interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

const embeddingEntitiesSQL = `
SELECT p.id, NULL::uuid, p.title, coalesce(c.title, ''),
	ARRAY(SELECT it.title FROM ref.interest_tag it WHERE (p.tag_mask::bigint >> it.bit) & 1 = 1 ORDER BY it.bit),
	ee.content_hash
FROM catalog.place p
LEFT JOIN ref.category c ON c.code = p.category
LEFT JOIN catalog.entity_embedding ee
	ON ee.place_id = p.id AND ee.city = p.city AND ee.model_key = $2 AND ee.model_version = $3
WHERE p.city = $1 AND p.is_active
UNION ALL
SELECT NULL::uuid, e.id, e.title, c.title,
	ARRAY(SELECT it.title FROM ref.interest_tag it WHERE (e.tag_mask::bigint >> it.bit) & 1 = 1 ORDER BY it.bit),
	ee.content_hash
FROM catalog.event e
JOIN ref.category c ON c.code = e.category
LEFT JOIN catalog.entity_embedding ee
	ON ee.event_id = e.id AND ee.city = e.city AND ee.model_key = $2 AND ee.model_version = $3
WHERE e.city = $1 AND e.is_active
ORDER BY 1, 2`

// EmbeddingEntities lists the city's active places and events with the text hash of their
// vector in the space, if they have one.
func EmbeddingEntities(ctx context.Context, db EmbeddingDB, city string, space ai.Space) ([]embedding.Entity, error) {
	rows, err := db.Query(ctx, embeddingEntitiesSQL, city, space.Key, space.Version)
	if err != nil {
		return nil, fmt.Errorf("read entities to embed: %w", err)
	}
	defer rows.Close()
	var out []embedding.Entity
	for rows.Next() {
		e := embedding.Entity{City: city}
		if err := rows.Scan(&e.Place, &e.Event, &e.Title, &e.Category, &e.Interests, &e.StoredHash); err != nil {
			return nil, fmt.Errorf("scan entity to embed: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read entities to embed: %w", err)
	}
	return out, nil
}

const upsertPlaceEmbeddingSQL = `
INSERT INTO catalog.entity_embedding (id, city, place_id, model_key, model_version, embedding, content_hash, created_at)
VALUES ($1, $2, $3, $4, $5, $6::vector, $7, now())
ON CONFLICT (place_id, city, model_key, model_version) WHERE place_id IS NOT NULL
DO UPDATE SET embedding = EXCLUDED.embedding, content_hash = EXCLUDED.content_hash, created_at = EXCLUDED.created_at`

const upsertEventEmbeddingSQL = `
INSERT INTO catalog.entity_embedding (id, city, event_id, model_key, model_version, embedding, content_hash, created_at)
VALUES ($1, $2, $3, $4, $5, $6::vector, $7, now())
ON CONFLICT (event_id, city, model_key, model_version) WHERE event_id IS NOT NULL
DO UPDATE SET embedding = EXCLUDED.embedding, content_hash = EXCLUDED.content_hash, created_at = EXCLUDED.created_at`

func UpsertEmbedding(ctx context.Context, db EmbeddingDB, space ai.Space, p embedding.Prepared, vector []float32) error {
	sql, id := upsertPlaceEmbeddingSQL, p.Entity.Place
	if id == nil {
		sql, id = upsertEventEmbeddingSQL, p.Entity.Event
	}
	if id == nil {
		return fmt.Errorf("entity %q has neither a place nor an event", p.Entity.Title)
	}
	_, err := db.Exec(ctx, sql, uuid.New().String(), p.Entity.City, id.String(), space.Key, space.Version, vectorLiteral(vector), p.Hash)
	if err != nil {
		return fmt.Errorf("write embedding of %s: %w", id, err)
	}
	return nil
}

func vectorLiteral(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(x), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}
