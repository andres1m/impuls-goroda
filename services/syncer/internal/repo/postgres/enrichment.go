package postgres

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/andres1m/impuls-goroda/pkg/catalogevent"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/enrich"
)

type EnrichmentStore struct {
	pool *pgxpool.Pool
}

func NewEnrichmentStore(pool *pgxpool.Pool) *EnrichmentStore {
	return &EnrichmentStore{pool: pool}
}

func (s *EnrichmentStore) Vocabulary(ctx context.Context) ([]enrich.Tag, error) {
	rows, err := s.pool.Query(ctx, `SELECT bit, code, title FROM ref.interest_tag ORDER BY bit`)
	if err != nil {
		return nil, fmt.Errorf("read interest tags: %w", err)
	}
	tags, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (enrich.Tag, error) {
		var t enrich.Tag
		return t, row.Scan(&t.Bit, &t.Code, &t.Title)
	})
	if err != nil {
		return nil, fmt.Errorf("read interest tags: %w", err)
	}
	return tags, nil
}

const enrichmentCandidatesSQL = `
SELECT p.id, NULL::uuid, p.title, coalesce(c.title, ''), p.tag_mask <> 0::bit(64), en.input_hash
FROM catalog.place p
LEFT JOIN ref.category c ON c.code = p.category
LEFT JOIN catalog.entity_enrichment en ON en.place_id = p.id AND en.city = p.city
WHERE p.city = $1 AND p.is_active
UNION ALL
SELECT NULL::uuid, e.id, e.title, c.title, e.tag_mask <> 0::bit(64), en.input_hash
FROM catalog.event e
JOIN ref.category c ON c.code = e.category
LEFT JOIN catalog.entity_enrichment en ON en.event_id = e.id AND en.city = e.city
WHERE e.city = $1 AND e.is_active
ORDER BY 1, 2`

// Candidates lists the city's active places and events with the input hash of their stored
// enrichment, if they have one.
func (s *EnrichmentStore) Candidates(ctx context.Context, city domain.City) ([]enrich.Candidate, error) {
	rows, err := s.pool.Query(ctx, enrichmentCandidatesSQL, string(city))
	if err != nil {
		return nil, fmt.Errorf("read entities to enrich: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (enrich.Candidate, error) {
		c := enrich.Candidate{City: string(city)}
		return c, row.Scan(&c.Place, &c.Event, &c.Title, &c.Category, &c.HasTags, &c.StoredHash)
	})
	if err != nil {
		return nil, fmt.Errorf("read entities to enrich: %w", err)
	}
	return out, nil
}

const upsertPlaceEnrichmentSQL = `
INSERT INTO catalog.entity_enrichment (id, city, place_id, model, input_hash, llm_tag_mask, enriched_at)
VALUES ($1, $2, $3, $4, $5, $6::bigint::bit(64), $7)
ON CONFLICT (place_id, city) WHERE place_id IS NOT NULL
DO UPDATE SET model = EXCLUDED.model, input_hash = EXCLUDED.input_hash,
	llm_tag_mask = EXCLUDED.llm_tag_mask, enriched_at = EXCLUDED.enriched_at`

const upsertEventEnrichmentSQL = `
INSERT INTO catalog.entity_enrichment (id, city, event_id, model, input_hash, llm_tag_mask, enriched_at)
VALUES ($1, $2, $3, $4, $5, $6::bigint::bit(64), $7)
ON CONFLICT (event_id, city) WHERE event_id IS NOT NULL
DO UPDATE SET model = EXCLUDED.model, input_hash = EXCLUDED.input_hash,
	llm_tag_mask = EXCLUDED.llm_tag_mask, enriched_at = EXCLUDED.enriched_at`

const addPlaceTagsSQL = `
UPDATE catalog.place SET tag_mask = tag_mask | $3::bigint::bit(64), updated_at = $4
WHERE id = $1 AND city = $2 AND (tag_mask | $3::bigint::bit(64)) <> tag_mask
RETURNING id::text`

const addEventTagsSQL = `
UPDATE catalog.event SET tag_mask = tag_mask | $3::bigint::bit(64), updated_at = $4
WHERE id = $1 AND city = $2 AND (tag_mask | $3::bigint::bit(64)) <> tag_mask
RETURNING place_id::text`

// Publish stores the results and adds their tags to places and events in one transaction under the
// city lock; the catalog revision grows only when some tag mask actually changed.
func (s *EnrichmentStore) Publish(
	ctx context.Context,
	city domain.City,
	model string,
	done []enrich.Enriched,
	at time.Time,
) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // a no-op after commit

	var revision int64
	if err := tx.QueryRow(ctx, `SELECT catalog_revision FROM ref.city WHERE code = $1 FOR UPDATE`, city).
		Scan(&revision); err != nil {
		return false, fmt.Errorf("lock city %s: %w", city, err)
	}
	touched := make(map[string]bool)
	for i := range done {
		c := &done[i].Candidate
		id, upsert, addTags := c.Place, upsertPlaceEnrichmentSQL, addPlaceTagsSQL
		if id == nil {
			id, upsert, addTags = c.Event, upsertEventEnrichmentSQL, addEventTagsSQL
		}
		if id == nil {
			return false, fmt.Errorf("entity %q has neither a place nor an event", c.Title)
		}
		if _, err := tx.Exec(ctx, upsert, uuid.New().String(), city, id.String(), model, done[i].Hash,
			done[i].Mask, at); err != nil {
			return false, fmt.Errorf("write enrichment of %s: %w", id, err)
		}
		places, err := returnedIDs(tx.Query(ctx, addTags, id.String(), city, done[i].Mask, at))
		if err != nil {
			return false, fmt.Errorf("add tags to %s: %w", id, err)
		}
		for _, p := range places {
			touched[p] = true
		}
	}
	if len(touched) > 0 {
		if err := tx.QueryRow(ctx, `
			UPDATE ref.city SET catalog_revision = catalog_revision + 1, updated_at = $2
			WHERE code = $1 RETURNING catalog_revision`, city, at).Scan(&revision); err != nil {
			return false, fmt.Errorf("bump catalog revision: %w", err)
		}
		if err := projectPlaces(ctx, tx, city, slices.Sorted(maps.Keys(touched)), revision, at); err != nil {
			return false, err
		}
		announcement := catalogevent.Invalidation{
			City:            string(city),
			CatalogRevision: revision,
			Reason:          catalogevent.ReasonIngest,
			PublishedAt:     at,
		}
		if err := EnqueueRevision(ctx, tx, &announcement); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	return len(touched) > 0, nil
}
