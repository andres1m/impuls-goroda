package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/resolve"
)

type placeData struct {
	pool *pgxpool.Pool
}

func (d placeData) Known(
	ctx context.Context,
	city domain.City,
	source domain.SourceKey,
	keys []string,
) (map[string]uuid.UUID, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT DISTINCT ON (f.value #>> '{}') f.value #>> '{}', l.place_id
		FROM integration.attribute_fact f
		JOIN integration.entity_link l ON l.id = f.entity_link_id
		JOIN integration.source_record r ON r.id = l.source_record_id
		JOIN integration.source s ON s.id = r.source_id
		WHERE f.attribute_name = $4 AND l.city = $1 AND s.source_key = $2 AND l.place_id IS NOT NULL
			AND (f.value #>> '{}') = ANY($3)
		ORDER BY f.value #>> '{}', l.resolved_at, l.id`, city, string(source), keys, resolve.AttrSourcePlaceID)
	if err != nil {
		return nil, fmt.Errorf("read resolved place keys: %w", err)
	}
	type pair struct {
		key string
		id  uuid.UUID
	}
	pairs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (pair, error) {
		var p pair
		return p, row.Scan(&p.key, &p.id)
	})
	if err != nil {
		return nil, fmt.Errorf("read resolved place keys: %w", err)
	}
	out := make(map[string]uuid.UUID, len(pairs))
	for _, p := range pairs {
		out[p.key] = p.id
	}
	return out, nil
}

func (d placeData) Existing(ctx context.Context, city domain.City, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	rows, err := d.pool.Query(ctx, `SELECT id FROM catalog.place WHERE city = $1 AND id = ANY($2)`, city, ids)
	if err != nil {
		return nil, fmt.Errorf("read existing places: %w", err)
	}
	found, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, fmt.Errorf("read existing places: %w", err)
	}
	out := make(map[uuid.UUID]bool, len(found))
	for _, id := range found {
		out[id] = true
	}
	return out, nil
}

// Nearby narrows by the bounding box first, which the GiST index serves, then measures on the spheroid.
func (d placeData) Nearby(
	ctx context.Context,
	city domain.City,
	source domain.SourceKey,
	lat, lon float64,
) ([]resolve.Candidate, error) {
	rows, err := d.pool.Query(ctx, `
		WITH probe AS (SELECT ST_SetSRID(ST_MakePoint($3, $2), 4326) AS pt)
		SELECT p.id, p.title, p.normalized_title, coalesce(p.category, ''),
			ST_Distance(p.coordinates::geography, probe.pt::geography)
		FROM probe, catalog.place p
		JOIN integration.source_record r ON r.id = p.card_source_record_id
		JOIN integration.source s ON s.id = r.source_id
		WHERE p.city = $1 AND p.is_active AND s.source_key <> $4 AND s.source_key <> 'synthetic'
			AND p.coordinates && ST_Expand(probe.pt, 0.001)
			AND ST_DWithin(p.coordinates::geography, probe.pt::geography, $5)
		ORDER BY 5, p.id`, city, lat, lon, string(source), resolve.Radius)
	if err != nil {
		return nil, fmt.Errorf("find nearby places: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (resolve.Candidate, error) {
		var c resolve.Candidate
		return c, row.Scan(&c.PlaceID, &c.Title, &c.NormalizedTitle, &c.Category, &c.Distance)
	})
	if err != nil {
		return nil, fmt.Errorf("find nearby places: %w", err)
	}
	return out, nil
}
