package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// Querier is satisfied by a pool and by a transaction.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Spatial answers metric questions about the catalog. Distances are measured on the spheroid.
type Spatial struct{ db Querier }

func NewSpatial(db Querier) *Spatial { return &Spatial{db: db} }

type NearbyPOI struct {
	PlaceID        domain.PlaceID
	DistanceMeters float64
}

// The cast must match the expression index on the leisure points, or the radius search scans the whole partition.
const nearbyPOIsSQL = `
	SELECT id, ST_Distance(coordinates::geography, center.point) AS distance
	FROM catalog.leisure_poi,
		(SELECT ST_SetSRID(ST_MakePoint($2::float8, $3::float8), 4326)::geography AS point) AS center
	WHERE city = $1
		AND is_active
		AND ST_DWithin(coordinates::geography, center.point, $4::float8)
	ORDER BY distance, id`

// NearbyPOIs returns the city's active points within the radius, inclusive, nearest first.
func (s *Spatial) NearbyPOIs(ctx context.Context, city string, center domain.Coordinate, radiusMeters float64) ([]NearbyPOI, error) {
	if err := validateCityPoint(city, center); err != nil {
		return nil, err
	}
	if !(radiusMeters > 0) || math.IsInf(radiusMeters, 0) {
		return nil, errors.New("radius must be positive and finite")
	}
	rows, err := s.db.Query(ctx, nearbyPOIsSQL, city, center.Longitude, center.Latitude, radiusMeters)
	if err != nil {
		return nil, fmt.Errorf("query nearby points: %w", err)
	}
	found, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (NearbyPOI, error) {
		var p NearbyPOI
		err := row.Scan(&p.PlaceID, &p.DistanceMeters)
		return p, err
	})
	if err != nil {
		return nil, fmt.Errorf("read nearby points: %w", err)
	}
	return found, nil
}

const cityCoversSQL = `
	SELECT boundary IS NOT NULL,
		COALESCE(ST_Covers(boundary, ST_SetSRID(ST_MakePoint($2::float8, $3::float8), 4326)), false)
	FROM ref.city
	WHERE code = $1`

// CityCovers reports whether the point lies inside the city or on its border. known is false
// while the city boundary is not loaded, so callers must not treat that as outside.
func (s *Spatial) CityCovers(ctx context.Context, city string, point domain.Coordinate) (covered, known bool, err error) {
	if err := validateCityPoint(city, point); err != nil {
		return false, false, err
	}
	err = s.db.QueryRow(ctx, cityCoversSQL, city, point.Longitude, point.Latitude).Scan(&known, &covered)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, fmt.Errorf("unknown city %q", city)
	}
	if err != nil {
		return false, false, fmt.Errorf("check city boundary: %w", err)
	}
	return covered, known, nil
}

func validateCityPoint(city string, point domain.Coordinate) error {
	if strings.TrimSpace(city) == "" {
		return errors.New("city is required")
	}
	return point.Validate()
}
