package postgres

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/uber/h3-go/v4"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func insertScenicPOI(t *testing.T, tx pgx.Tx, city string, cell h3.Cell, active bool, categories ...string) {
	t.Helper()
	ctx := t.Context()
	id := newPlaceID(t)
	centre, err := cell.LatLng()
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO catalog.place (id, city, title, normalized_title, category, coordinates, data_mode, is_active, review_required, created_at, updated_at)
		VALUES ($1, $2, 'Fixture', 'fixture', $3, ST_SetSRID(ST_MakePoint($4, $5), 4326), 'synthetic', true, false, now(), now())`,
		id, city, categories[0], centre.Lng, centre.Lat)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO catalog.leisure_poi (id, city, title, normalized_title, categories, tag_mask, data_mode, coordinates,
			h3_res8, h3_res11, base_score, catalog_revision, is_active, updated_at)
		SELECT id, city, title, normalized_title, $3, 0::bit(64), data_mode, coordinates, $4, 0, 1, 0, $5, now()
		FROM catalog.place WHERE id = $1 AND city = $2`,
		id, city, categories, int64(cell), active)
	if err != nil {
		t.Fatal(err)
	}
}

func TestScenicPlacesIntegration(t *testing.T) {
	ctx, tx := fixtureTx(t)
	point := func(c domain.Coordinate) h3.Cell {
		cell, err := h3.LatLngToCell(h3.NewLatLng(c.Latitude, c.Longitude), 8)
		if err != nil {
			t.Fatal(err)
		}
		return cell
	}
	park := point(permCenter)
	square := point(domain.Coordinate{Longitude: permCenter.Longitude + 0.05, Latitude: permCenter.Latitude})
	before, err := NewCatalog(tx).ScenicPlaces(ctx, "perm")
	if err != nil {
		t.Fatal(err)
	}
	insertScenicPOI(t, tx, "perm", park, true, "walk")
	insertScenicPOI(t, tx, "perm", park, true, "tourism", "culture")
	insertScenicPOI(t, tx, "perm", park, true, "culture")
	insertScenicPOI(t, tx, "perm", park, false, "walk")
	insertScenicPOI(t, tx, "perm", square, true, "walk")
	insertScenicPOI(t, tx, "moscow", park, true, "walk")

	after, err := NewCatalog(tx).ScenicPlaces(ctx, "perm")
	if err != nil {
		t.Fatal(err)
	}
	if got := after[park] - before[park]; got != 2 {
		t.Fatalf("park cell gained %d scenic places, want 2", got)
	}
	if got := after[square] - before[square]; got != 1 {
		t.Fatalf("square cell gained %d scenic places, want 1", got)
	}
}
