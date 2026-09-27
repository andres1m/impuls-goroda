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
SELECT place_id, event_id, session_id
FROM catalog.entity_embedding
WHERE city = $1 AND model_key = $2 AND model_version = $3
ORDER BY (embedding <=> $4::vector) + 0
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
