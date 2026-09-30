package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/coverage"
)

type Coverage struct {
	pool *pgxpool.Pool
}

func NewCoverage(pool *pgxpool.Pool) *Coverage {
	return &Coverage{pool: pool}
}

const coverageSourcesSQL = `
SELECT src.source_key, rc.code, coalesce(string_agg(DISTINCT sr.data_mode, ','), ''),
	c.last_attempt_at, c.last_success_at, coalesce(c.last_error_code, ''),
	(c.published_watermark->>'fetched_at')::timestamptz, (c.materialized_watermark->>'fetched_at')::timestamptz,
	count(ri.id) FILTER (WHERE ri.processing_state = 'pending'),
	count(ri.id) FILTER (WHERE ri.processing_state = 'applied'),
	count(ri.id) FILTER (WHERE ri.processing_state = 'failed'),
	count(ri.id) FILTER (WHERE ri.processing_state = 'quarantined'),
	max(ri.fetched_at), max(ri.source_updated_at)
FROM integration.source src
CROSS JOIN ref.city rc
LEFT JOIN integration.sync_cursor c ON c.source_id = src.id AND c.city = rc.code
LEFT JOIN integration.source_record sr ON sr.source_id = src.id AND sr.city = rc.code
LEFT JOIN integration.raw_ingest ri ON ri.source_record_id = sr.id
WHERE $1 = '' OR rc.code = $1
GROUP BY src.source_key, rc.code, c.source_id, c.city
HAVING count(ri.id) > 0 OR c.source_id IS NOT NULL
ORDER BY src.source_key, rc.code`

const coverageAttributesSQL = `
SELECT city, target_kind, attribute_name, count(*), min(fetched_at), max(fetched_at), max(source_updated_at),
	count(verified_at), max(verified_at)
FROM integration.attribute_fact
WHERE is_selected AND ($1 = '' OR city = $1)
GROUP BY city, target_kind, attribute_name
ORDER BY city, target_kind, attribute_name`

// Places and events count as they are shown; sessions are the ones of active events that have not ended by $2.
const coverageCatalogSQL = `
WITH rows AS (
	SELECT city, data_mode, 1 AS places, 0 AS events, 0 AS sessions, 0 AS cancelled, 0 AS unknown, 0 AS observed,
		NULL::timestamptz AS latest
	FROM catalog.place WHERE is_active AND ($1 = '' OR city = $1)
	UNION ALL
	SELECT city, data_mode, 0, 1, 0, 0, 0, 0, NULL
	FROM catalog.event WHERE is_active AND ($1 = '' OR city = $1)
	UNION ALL
	SELECT s.city, s.data_mode, 0, 0, 1, (s.availability_status = 'cancelled')::int,
		(s.availability_status = 'unknown')::int, (s.availability_observed_at IS NOT NULL)::int,
		s.availability_observed_at
	FROM catalog.session s
	JOIN catalog.event e ON e.id = s.event_id AND e.city = s.city AND e.is_active
	WHERE s.ends_at > $2 AND ($1 = '' OR s.city = $1)
)
SELECT city, data_mode, sum(places), sum(events), sum(sessions), sum(cancelled), sum(unknown), sum(observed),
	max(latest)
FROM rows GROUP BY city, data_mode ORDER BY city, data_mode`

// Read collects the report for one city, or for all cities when city is empty.
func (c *Coverage) Read(ctx context.Context, city string, now time.Time) (coverage.Report, error) {
	var report coverage.Report
	sources, err := c.pool.Query(ctx, coverageSourcesSQL, city)
	if err != nil {
		return report, fmt.Errorf("read source coverage: %w", err)
	}
	report.Sources, err = pgx.CollectRows(sources, pgx.RowToStructByPos[coverage.Source])
	if err != nil {
		return report, fmt.Errorf("read source coverage: %w", err)
	}
	attributes, err := c.pool.Query(ctx, coverageAttributesSQL, city)
	if err != nil {
		return report, fmt.Errorf("read attribute coverage: %w", err)
	}
	report.Attributes, err = pgx.CollectRows(attributes, pgx.RowToStructByPos[coverage.Attribute])
	if err != nil {
		return report, fmt.Errorf("read attribute coverage: %w", err)
	}
	catalog, err := c.pool.Query(ctx, coverageCatalogSQL, city, now)
	if err != nil {
		return report, fmt.Errorf("read catalog coverage: %w", err)
	}
	report.Catalog, err = pgx.CollectRows(catalog, pgx.RowToStructByPos[coverage.Catalog])
	if err != nil {
		return report, fmt.Errorf("read catalog coverage: %w", err)
	}
	return report, nil
}
