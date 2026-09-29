package postgres

import (
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/materialize"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/normalize"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/resolve"
)

const (
	testLat = 58.2000
	testLon = 56.4000
)

func draftAt(externalID, category, title string, lat, lon float64, tags ...string) normalize.PlaceDraft {
	d := draft(externalID, category, title, tags...)
	d.Lat, d.Lon = lat, lon
	return d
}

// metersEast is the longitude shift that moves a point at testLat by about m meters.
func metersEast(m float64) float64 { return m / 58900 }

func TestNearbyFindsOtherSourcesOnly(t *testing.T) {
	a := newMaterializeFixture(t)
	b := newMaterializeFixture(t)
	base := time.Now().UTC().Truncate(time.Microsecond)
	raw := a.save(t, "n1", `{"v":1}`, base)
	o := &materialize.Outcome{Apply: []materialize.Normalized{
		{Raw: a.pending(t, raw.ID)[0], Place: draftAt("n1", "gastro", "Кофейня Центральная", testLat, testLon)},
	}}
	if _, _, err := a.store.Publish(a.ctx, domain.Perm, o, base); err != nil {
		t.Fatal(err)
	}

	data := placeData{pool: a.pool}
	got, err := data.Nearby(a.ctx, domain.Perm, b.source, testLat, testLon+metersEast(30))
	if err != nil || len(got) != 1 || got[0].Title != "Кофейня Центральная" || got[0].Distance < 25 || got[0].Distance > 35 {
		t.Fatalf("candidates %+v %v", got, err)
	}
	if got, err = data.Nearby(a.ctx, domain.Perm, b.source, testLat, testLon+metersEast(80)); err != nil || len(got) != 0 {
		t.Fatalf("a place 80 m away is a candidate: %+v %v", got, err)
	}
	if got, err = data.Nearby(a.ctx, domain.Perm, a.source, testLat, testLon); err != nil || len(got) != 0 {
		t.Fatalf("a place of the same source is a candidate: %+v %v", got, err)
	}
}

func TestTrigramAgreesWithPostgres(t *testing.T) {
	f := newMaterializeFixture(t)
	for _, c := range [][2]string{
		{"йоши тоши", "йоши тоши"}, {"ланчи и бранчи", "ланчи-бранчи"},
		{"пермский театр оперы и балета", "пермский театр оперы и балета чайковского"},
		{"кофейня", "кофейня на набережной"}, {"третьяковская галерея", "государственная третьяковская галерея"},
	} {
		var want float64
		if err := f.pool.QueryRow(f.ctx, `SELECT similarity($1, $2)::float8`, c[0], c[1]).Scan(&want); err != nil {
			t.Fatal(err)
		}
		if got := resolve.Trigram(c[0], c[1]); got-want > 1e-6 || want-got > 1e-6 {
			t.Errorf("Trigram(%q, %q) = %v, pg_trgm says %v", c[0], c[1], got, want)
		}
	}
}

func TestSyntheticPlacesAreNotCandidatesIntegration(t *testing.T) {
	f := newMaterializeFixture(t)
	var lat, lon float64
	err := f.pool.QueryRow(f.ctx, `
		SELECT ST_Y(p.coordinates), ST_X(p.coordinates) FROM catalog.place p
		JOIN integration.source_record r ON r.id = p.card_source_record_id
		JOIN integration.source s ON s.id = r.source_id
		WHERE p.city = 'perm' AND p.is_active AND s.source_key = 'synthetic' LIMIT 1`).Scan(&lat, &lon)
	if err != nil {
		t.Skip("no synthetic place in the test database")
	}
	got, err := placeData{pool: f.pool}.Nearby(f.ctx, domain.Perm, f.source, lat, lon)
	if err != nil {
		t.Fatal(err)
	}
	for i := range got {
		var key string
		if err := f.pool.QueryRow(f.ctx, `SELECT s.source_key FROM catalog.place p
			JOIN integration.source_record r ON r.id = p.card_source_record_id
			JOIN integration.source s ON s.id = r.source_id WHERE p.id = $1`, got[i].PlaceID).Scan(&key); err != nil || key == "synthetic" {
			t.Fatalf("synthetic place offered as a candidate: %+v (%v)", got[i], err)
		}
	}
}
