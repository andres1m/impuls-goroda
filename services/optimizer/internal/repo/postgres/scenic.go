package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/uber/h3-go/v4"
)

const scenicPlacesSQL = `
	SELECT h3_res8, count(*)
	FROM catalog.leisure_poi
	WHERE city = $1 AND is_active AND categories && ARRAY['walk', 'tourism']::text[]
	GROUP BY h3_res8`

// ScenicPlaces counts the city's active walking and tourist places per H3 resolution 8 cell.
// A stored index that is not a valid cell of that resolution is skipped.
func (c *Catalog) ScenicPlaces(ctx context.Context, city string) (map[h3.Cell]int, error) {
	rows, err := c.db.Query(ctx, scenicPlacesSQL, city)
	if err != nil {
		return nil, wrapDBError("query scenic places", err)
	}
	places := map[h3.Cell]int{}
	var index int64
	var count int
	_, err = pgx.ForEachRow(rows, []any{&index, &count}, func() error {
		if cell := h3.Cell(index); cell.IsValid() && cell.Resolution() == 8 {
			places[cell] = count
		}
		return nil
	})
	if err != nil {
		return nil, wrapDBError("read scenic places", err)
	}
	return places, nil
}
