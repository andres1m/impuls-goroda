package postgres

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"math"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// fixtureTx runs the test inside a transaction that is always rolled back, so fixtures never stay in the database.
func fixtureTx(t *testing.T) (context.Context, pgx.Tx) {
	t.Helper()
	databaseURL := os.Getenv("OPTIMIZER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("OPTIMIZER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return ctx, tx
}

func newPlaceID(t *testing.T) domain.PlaceID {
	t.Helper()
	var id domain.PlaceID
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	return id
}

// insertPOI places a point exactly meters away from center along the azimuth, measured on the spheroid.
func insertPOI(ctx context.Context, t *testing.T, tx pgx.Tx, city string, center domain.Coordinate, meters, azimuth float64, active bool) domain.PlaceID {
	t.Helper()
	id := newPlaceID(t)
	_, err := tx.Exec(ctx, `
		INSERT INTO catalog.place (id, city, title, normalized_title, category, coordinates, data_mode, is_active, review_required, created_at, updated_at)
		VALUES ($1, $2, 'Fixture', 'fixture', 'culture',
			ST_Project(ST_SetSRID(ST_MakePoint($3, $4), 4326)::geography, $5::float8, radians($6::float8))::geometry,
			'synthetic', $7, false, now(), now())`,
		id, city, center.Longitude, center.Latitude, meters, azimuth, active)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO catalog.leisure_poi (id, city, title, normalized_title, categories, tag_mask, data_mode, coordinates,
			h3_res8, h3_res11, base_score, catalog_revision, is_active, updated_at)
		SELECT id, city, title, normalized_title, ARRAY['culture'], 0::bit(64), data_mode, coordinates, 0, 0, 1, 0, $3, now()
		FROM catalog.place WHERE id = $1 AND city = $2`,
		id, city, active)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestNearbyPOIsRadiiIntegration(t *testing.T) {
	ctx, tx := fixtureTx(t)
	type fixture struct {
		id     domain.PlaceID
		meters float64
	}
	var fixtures []fixture
	for i, r := range []float64{300, 500, 800, 1000} {
		azimuth := float64(i * 90)
		for _, meters := range []float64{r - 1, r + 1} {
			fixtures = append(fixtures, fixture{insertPOI(ctx, t, tx, "perm", permCenter, meters, azimuth, true), meters})
		}
	}
	inactive := insertPOI(ctx, t, tx, "perm", permCenter, 10, 0, false)
	moscow := insertPOI(ctx, t, tx, "moscow", permCenter, 10, 0, true)

	s := NewSpatial(tx)
	for _, r := range []float64{300, 500, 800, 1000} {
		found, err := s.NearbyPOIs(ctx, "perm", permCenter, r)
		if err != nil {
			t.Fatal(err)
		}
		byID := map[domain.PlaceID]float64{}
		for _, p := range found {
			byID[p.PlaceID] = p.DistanceMeters
		}
		for _, f := range fixtures {
			distance, ok := byID[f.id]
			if ok != (f.meters <= r) {
				t.Errorf("radius %.0f: point at %.0f m returned=%v", r, f.meters, ok)
			}
			if ok && math.Abs(distance-f.meters) > 0.01 {
				t.Errorf("radius %.0f: distance %.4f, want %.0f", r, distance, f.meters)
			}
		}
		if _, ok := byID[inactive]; ok {
			t.Errorf("radius %.0f: inactive point returned", r)
		}
		if _, ok := byID[moscow]; ok {
			t.Errorf("radius %.0f: point of another city returned", r)
		}
		if !slices.IsSortedFunc(found, func(a, b NearbyPOI) int { return compareFloat(a.DistanceMeters, b.DistanceMeters) }) {
			t.Errorf("radius %.0f: not ordered by distance", r)
		}
	}
}

func compareFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

type planNode struct {
	NodeType     string     `json:"Node Type"`
	RelationName string     `json:"Relation Name"`
	IndexName    string     `json:"Index Name"`
	IndexCond    string     `json:"Index Cond"`
	Plans        []planNode `json:"Plans"`
}

func (n planNode) walk(visit func(planNode)) {
	visit(n)
	for _, child := range n.Plans {
		child.walk(visit)
	}
}

func TestNearbyPOIsUsesGeographyIndexIntegration(t *testing.T) {
	ctx, tx := fixtureTx(t)
	// With a handful of rows the planner rightly prefers a sequential scan; this proves the index can serve the query.
	if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan = off"); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	err := tx.QueryRow(ctx, "EXPLAIN (FORMAT JSON) "+nearbyPOIsSQL, "perm", permCenter.Longitude, permCenter.Latitude, 500.0).Scan(&raw)
	if err != nil {
		t.Fatal(err)
	}
	var plans []struct {
		Plan planNode `json:"Plan"`
	}
	if err := json.Unmarshal(raw, &plans); err != nil || len(plans) != 1 {
		t.Fatalf("plan %s: %v", raw, err)
	}
	var relations []string
	indexed := false
	plans[0].Plan.walk(func(n planNode) {
		if n.RelationName != "" {
			relations = append(relations, n.RelationName)
		}
		if n.IndexName != "" && strings.Contains(n.IndexCond, "geography") {
			indexed = true
		}
	})
	if len(relations) == 0 || slices.ContainsFunc(relations, func(r string) bool { return r != "leisure_poi_perm" }) {
		t.Errorf("scanned relations %v, want only the city partition", relations)
	}
	if !indexed {
		t.Errorf("radius condition does not use the geography index: %s", raw)
	}
}

func TestCityCoversIntegration(t *testing.T) {
	ctx, tx := fixtureTx(t)
	_, err := tx.Exec(ctx, `
		UPDATE ref.city SET boundary = CASE code
			WHEN 'perm' THEN ST_Multi(ST_MakeEnvelope(56.0, 57.9, 56.5, 58.1, 4326))
			ELSE NULL END`)
	if err != nil {
		t.Fatal(err)
	}
	s := NewSpatial(tx)
	cases := []struct {
		city           string
		point          domain.Coordinate
		covered, known bool
	}{
		{"perm", permCenter, true, true},
		{"perm", domain.Coordinate{Longitude: 56.5, Latitude: 58.0}, true, true},
		{"perm", domain.Coordinate{Longitude: 57.0, Latitude: 58.0}, false, true},
		{"moscow", domain.Coordinate{Longitude: 37.62, Latitude: 55.75}, false, false},
	}
	for _, tc := range cases {
		covered, known, err := s.CityCovers(ctx, tc.city, tc.point)
		if err != nil || covered != tc.covered || known != tc.known {
			t.Errorf("%s %+v: covered=%v known=%v err=%v", tc.city, tc.point, covered, known, err)
		}
	}
	if _, _, err := s.CityCovers(ctx, "kazan", permCenter); err == nil {
		t.Error("unknown city accepted")
	}
}
