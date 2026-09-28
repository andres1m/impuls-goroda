package postgres

import (
	"context"
	"strconv"
	"strings"

	"github.com/andres1m/impuls-goroda/pkg/ai"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/semantic"
)

// The HNSW index is shared by every city and model and returns only its first candidates
// before the filter, so the distance is wrapped to force an exact scan of the filtered rows.
const nearestEntitiesSQL = `
SELECT e.place_id, e.event_id, e.session_id
FROM catalog.entity_embedding e
WHERE e.city = $1 AND e.model_key = $2 AND e.model_version = $3
  AND (
    (e.place_id IS NOT NULL AND EXISTS (
      SELECT 1 FROM catalog.place p
      WHERE p.city = e.city AND p.id = e.place_id AND p.is_active
    ))
    OR (e.event_id IS NOT NULL AND EXISTS (
      SELECT 1 FROM catalog.event ev
      JOIN catalog.place p ON p.city = ev.city AND p.id = ev.place_id
      WHERE ev.city = e.city AND ev.id = e.event_id AND ev.is_active AND p.is_active
    ))
    OR (e.session_id IS NOT NULL AND EXISTS (
      SELECT 1 FROM catalog.session s
      JOIN catalog.event ev ON ev.city = s.city AND ev.id = s.event_id
      JOIN catalog.place p ON p.city = ev.city AND p.id = ev.place_id
      WHERE s.city = e.city AND s.id = e.session_id
        AND s.availability_status NOT IN ('cancelled', 'sold_out')
        AND ev.is_active AND p.is_active
    ))
  )
ORDER BY (e.embedding <=> $4::vector) + 0
LIMIT $5`

func (c *Catalog) NearestEntities(ctx context.Context, city string, space ai.Space, vector []float32, limit int) ([]semantic.Match, error) {
	rows, err := c.db.Query(ctx, nearestEntitiesSQL, city, space.Key, space.Version, vectorLiteral(vector), limit)
	if err != nil {
		return nil, wrapDBError("query nearest entities", err)
	}
	defer rows.Close()
	var out []semantic.Match
	for rows.Next() {
		var m semantic.Match
		if err := rows.Scan(&m.Place, &m.Event, &m.Session); err != nil {
			return nil, wrapDBError("scan nearest entity", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapDBError("read nearest entities", err)
	}
	return out, nil
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
