package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/materialize"
)

const permSquare = `MULTIPOLYGON(((56 57.9,56.5 57.9,56.5 58.1,56 58.1,56 57.9)))`

// setBoundary replaces Perm's boundary for the test and puts the previous one back afterwards.
func (f *materializeFixture) setBoundary(t *testing.T, wkt *string) {
	t.Helper()
	var previous *string
	if err := f.pool.QueryRow(f.ctx, `SELECT ST_AsText(boundary) FROM ref.city WHERE code = 'perm'`).
		Scan(&previous); err != nil {
		t.Fatal(err)
	}
	const update = `UPDATE ref.city SET boundary = ST_Multi(ST_GeomFromText($1, 4326)) WHERE code = 'perm'`
	t.Cleanup(func() {
		if _, err := f.pool.Exec(context.Background(), update, previous); err != nil {
			t.Errorf("restore boundary: %v", err)
		}
	})
	if _, err := f.pool.Exec(f.ctx, update, wkt); err != nil {
		t.Fatal(err)
	}
}

func TestOutsideBoundaryIntegration(t *testing.T) {
	f := newMaterializeFixture(t)
	points := []materialize.Point{{Lat: 58.0105, Lon: 56.2294}, {Lat: 58.0105, Lon: 57.5}}

	f.setBoundary(t, nil)
	if _, known, err := f.store.OutsideBoundary(f.ctx, domain.Perm, points); err != nil || known {
		t.Fatalf("without boundary known=%v err=%v", known, err)
	}
	square := permSquare
	f.setBoundary(t, &square)
	outside, known, err := f.store.OutsideBoundary(f.ctx, domain.Perm, points)
	if err != nil || !known || len(outside) != 2 || outside[0] || !outside[1] {
		t.Fatalf("outside %v known %v err %v", outside, known, err)
	}
	if _, _, err := f.store.OutsideBoundary(f.ctx, domain.City("kazan"), points); err == nil {
		t.Fatal("unknown city answered")
	}
}

type quarantineRow struct {
	reason, state string
	resolved      bool
}

func (f *materializeFixture) quarantine(t *testing.T, rawID string) []quarantineRow {
	t.Helper()
	rows, err := f.pool.Query(f.ctx, `SELECT reason_code, state, resolved_at IS NOT NULL FROM integration.quarantine
		WHERE raw_ingest_id = $1 ORDER BY reason_code`, rawID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []quarantineRow
	for rows.Next() {
		var r quarantineRow
		if err := rows.Scan(&r.reason, &r.state, &r.resolved); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func (f *materializeFixture) deadLetters(t *testing.T, rawID string) []ingest.DeadLetter {
	t.Helper()
	rows, err := f.pool.Query(f.ctx, `SELECT payload FROM integration.change_delivery
		WHERE raw_ingest_id = $1 AND destination = $2 AND event_type = $3`,
		rawID, ingest.DeadLetterDestination, ingest.DeadLetterEventType)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []ingest.DeadLetter
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			t.Fatal(err)
		}
		letter, err := ingest.DecodeDeadLetter(payload)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, letter)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestQuarantineIntegration(t *testing.T) {
	f := newMaterializeFixture(t)
	base := time.Now().UTC().Truncate(time.Microsecond)
	good := f.save(t, "node/1", `{"v":1}`, base)
	broken := f.save(t, "node/2", `not json`, base.Add(time.Second))
	far := f.save(t, "node/3", `{"v":3}`, base.Add(2*time.Second))
	raws := f.pending(t, good.ID, broken.ID, far.ID)
	outcome := &materialize.Outcome{
		Apply: []materialize.Normalized{{Raw: raws[0], Place: draft("node/1", "gastro", "Кофейня", "gastro_coffee")}},
		Quarantined: []materialize.Quarantined{
			{Raw: raws[1], Reason: domain.InvalidSchema, Details: materialize.QuarantineDetails{Code: "bad_payload"}},
			{Raw: raws[2], Reason: domain.GeoDiscrepancy,
				Details: materialize.QuarantineDetails{Check: "outside_city_boundary", Lat: 58.01, Lon: 57.5}},
		},
	}
	if _, published, err := f.store.Publish(f.ctx, domain.Perm, outcome, base); err != nil || !published {
		t.Fatalf("published %v, err %v", published, err)
	}
	f.assertQuarantined(t, good.ID, broken.ID, far.ID, base)

	if _, _, err := f.store.Publish(f.ctx, domain.Perm, outcome, base); err != nil {
		t.Fatal(err)
	}
	if len(f.quarantine(t, broken.ID)) != 1 || len(f.deadLetters(t, broken.ID)) != 1 {
		t.Fatal("a repeated batch quarantined twice")
	}
	f.assertQuarantineOnlyBatch(t, base)
	f.assertAcceptedVersionResolves(t, broken.ID, far.ID, base)
}

func (f *materializeFixture) assertQuarantined(t *testing.T, goodID, brokenID, farID string, base time.Time) {
	t.Helper()
	if f.state(t, goodID) != "applied" || f.state(t, brokenID) != "quarantined" || f.state(t, farID) != "quarantined" {
		t.Fatalf("states %s %s %s", f.state(t, goodID), f.state(t, brokenID), f.state(t, farID))
	}
	if got := f.quarantine(t, brokenID); len(got) != 1 || got[0] != (quarantineRow{reason: "InvalidSchema", state: "open"}) {
		t.Fatalf("broken quarantine %+v", got)
	}
	if got := f.quarantine(t, farID); len(got) != 1 || got[0].reason != "GEO_DISCREPANCY_QUARANTINE" {
		t.Fatalf("far quarantine %+v", got)
	}
	var check string
	if err := f.pool.QueryRow(f.ctx, `SELECT details->>'check' FROM integration.quarantine WHERE raw_ingest_id = $1`,
		farID).Scan(&check); err != nil || check != "outside_city_boundary" {
		t.Fatalf("details check %q, %v", check, err)
	}
	letters := f.deadLetters(t, brokenID)
	want := ingest.DeadLetter{Version: ingest.DeadLetterVersion, Stage: ingest.StagePayload, Reason: domain.InvalidSchema,
		RawIngestID: brokenID, Source: f.source, City: domain.Perm, ExternalID: "node/2", Error: "bad_payload"}
	if len(letters) != 1 || letters[0].Version != want.Version || letters[0].Stage != want.Stage ||
		letters[0].RawIngestID != want.RawIngestID || letters[0].Source != want.Source || letters[0].City != want.City ||
		letters[0].ExternalID != want.ExternalID || letters[0].Error != want.Error || letters[0].Reason != want.Reason {
		t.Fatalf("dead letters %+v", letters)
	}
	if len(f.deadLetters(t, farID)) != 0 {
		t.Fatal("a place outside the city went to the dead letter topic")
	}
	if w := f.watermark(t); w == nil || !w.Equal(base) {
		t.Fatalf("watermark %v passed a quarantined record", w)
	}
}

func (f *materializeFixture) assertQuarantineOnlyBatch(t *testing.T, base time.Time) {
	t.Helper()
	also := f.save(t, "node/4", `also not json`, base.Add(3*time.Second))
	raws := f.pending(t, also.ID)
	before := f.revision(t)
	_, published, err := f.store.Publish(f.ctx, domain.Perm, &materialize.Outcome{Quarantined: []materialize.Quarantined{
		{Raw: raws[0], Reason: domain.InvalidSchema, Details: materialize.QuarantineDetails{Code: "bad_payload"}},
	}}, base)
	if err != nil || published || f.revision(t) != before || f.state(t, also.ID) != "quarantined" {
		t.Fatalf("quarantine-only batch published %v, revision %d → %d, err %v", published, before, f.revision(t), err)
	}
}

func (f *materializeFixture) assertAcceptedVersionResolves(t *testing.T, brokenID, farID string, base time.Time) {
	t.Helper()
	fixed := f.save(t, "node/2", `{"v":"fixed"}`, base.Add(4*time.Second))
	raws := f.pending(t, fixed.ID)
	if _, _, err := f.store.Publish(f.ctx, domain.Perm, &materialize.Outcome{Apply: []materialize.Normalized{
		{Raw: raws[0], Place: draft("node/2", "gastro", "Кафе", "gastro_coffee")},
	}}, base); err != nil {
		t.Fatal(err)
	}
	if got := f.quarantine(t, brokenID); len(got) != 1 || got[0] != (quarantineRow{reason: "InvalidSchema",
		state: "resolved", resolved: true}) {
		t.Fatalf("broken quarantine after a fixed version %+v", got)
	}
	if got := f.quarantine(t, farID); len(got) != 1 || got[0].state != "open" {
		t.Fatalf("another record's quarantine changed %+v", got)
	}
}
