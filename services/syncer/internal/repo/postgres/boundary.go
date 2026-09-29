package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

type BoundaryStats struct {
	Parts   int
	AreaKm2 float64
}

// SaveBoundary builds the city's area from its boundary lines; lines that do not close into a valid
// area leave the stored boundary as it was.
func SaveBoundary(
	ctx context.Context,
	db rowQuerier,
	city domain.City,
	lines [][][2]float64,
	at time.Time,
) (BoundaryStats, error) {
	geometry, err := json.Marshal(map[string]any{"type": "MultiLineString", "coordinates": lines})
	if err != nil {
		return BoundaryStats{}, fmt.Errorf("encode boundary lines: %w", err)
	}
	var s BoundaryStats
	err = db.QueryRow(ctx, `
		WITH built AS (SELECT ST_Multi(ST_BuildArea(ST_SetSRID(ST_GeomFromGeoJSON($2), 4326))) AS g)
		UPDATE ref.city c SET boundary = b.g, updated_at = $3
		FROM built b
		WHERE c.code = $1 AND b.g IS NOT NULL AND NOT ST_IsEmpty(b.g) AND ST_IsValid(b.g)
		RETURNING ST_NumGeometries(b.g), ST_Area(b.g::geography) / 1e6`, city, string(geometry), at).
		Scan(&s.Parts, &s.AreaKm2)
	if errors.Is(err, pgx.ErrNoRows) {
		return BoundaryStats{}, fmt.Errorf("boundary of %s: the lines do not enclose a valid area", city)
	}
	if err != nil {
		return BoundaryStats{}, fmt.Errorf("save boundary of %s: %w", city, err)
	}
	return s, nil
}
