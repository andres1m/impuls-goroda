package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/materialize"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/normalize"
)

type materializeFixture struct {
	ctx      context.Context
	pool     *pgxpool.Pool
	landing  *Landing
	store    *MaterializeStore
	sourceID ingest.SourceID
	source   domain.SourceKey
}

func newMaterializeFixture(t *testing.T) *materializeFixture {
	databaseURL := os.Getenv("SYNCER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("SYNCER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	f := &materializeFixture{ctx: ctx, pool: pool, landing: NewLanding(pool), store: NewMaterializeStore(func() *pgxpool.Pool { return pool }),
		source: domain.SourceKey("test_" + randomSuffix(t))}
	f.sourceID, err = f.landing.EnsureSource(ctx, domain.Source{Key: f.source, Name: "Materialize test", AccessMode: domain.AccessAPI, SchemaVersion: "1", DataMode: domain.Live})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.cleanup(t) })
	return f
}

func (f *materializeFixture) cleanup(t *testing.T) {
	for _, q := range []string{
		`DELETE FROM catalog.leisure_poi p USING catalog.place c, integration.source_record r
			WHERE p.id = c.id AND p.city = c.city AND c.card_source_record_id = r.id AND r.source_id = $1`,
		`DELETE FROM catalog.place c USING integration.source_record r WHERE c.card_source_record_id = r.id AND r.source_id = $1`,
	} {
		if _, err := f.pool.Exec(context.Background(), q, uuidParam(f.sourceID)); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	}
	deleteSource(t, f.pool, f.sourceID)
}

// save lands a record and returns its raw ingest id.
func (f *materializeFixture) save(t *testing.T, externalID, payload string, at time.Time) materialize.Raw {
	t.Helper()
	rec := domain.RawRecord{ExternalID: externalID, SourceURL: "https://example.test/" + externalID, Payload: []byte(payload), ContentType: "application/json"}
	if _, err := f.landing.SaveRecord(f.ctx, f.sourceID, domain.Perm, domain.Live, rec, at); err != nil {
		t.Fatal(err)
	}
	envelopes, err := f.landing.Unpublished(f.ctx, f.sourceID, domain.Perm)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(envelopes) - 1; i >= 0; i-- {
		if envelopes[i].ExternalID == externalID {
			return materialize.Raw{ID: envelopes[i].RawIngestID}
		}
	}
	t.Fatalf("no raw ingest for %s", externalID)
	return materialize.Raw{}
}

func (f *materializeFixture) pending(t *testing.T, ids ...string) []materialize.Raw {
	t.Helper()
	raws, err := f.store.PendingBatch(f.ctx, domain.Perm, ids)
	if err != nil {
		t.Fatal(err)
	}
	return raws
}

func (f *materializeFixture) revision(t *testing.T) int64 {
	t.Helper()
	var r int64
	if err := f.pool.QueryRow(f.ctx, `SELECT catalog_revision FROM ref.city WHERE code = 'perm'`).Scan(&r); err != nil {
		t.Fatal(err)
	}
	return r
}

func (f *materializeFixture) state(t *testing.T, rawID string) string {
	t.Helper()
	var s string
	if err := f.pool.QueryRow(f.ctx, `SELECT processing_state FROM integration.raw_ingest WHERE id = $1`, rawID).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func (f *materializeFixture) watermark(t *testing.T) *time.Time {
	t.Helper()
	var at *time.Time
	err := f.pool.QueryRow(f.ctx, `SELECT (materialized_watermark->>'fetched_at')::timestamptz FROM integration.sync_cursor
		WHERE source_id = $1 AND city = 'perm'`, uuidParam(f.sourceID)).Scan(&at)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func draft(externalID, category, title string, tags ...string) normalize.PlaceDraft {
	return normalize.PlaceDraft{ExternalID: externalID, Title: title, NormalizedTitle: normalize.NormalizedTitle(title),
		Category: category, Tags: tags, Lat: 58.0105, Lon: 56.2294, OpeningRules: []byte(`{}`)}
}

func TestMaterializeStoreIntegration(t *testing.T) {
	f := newMaterializeFixture(t)
	base := time.Now().UTC().Truncate(time.Microsecond)
	cafe := f.save(t, "node/1", `{"v":1}`, base)
	museum := f.save(t, "node/2", `{"v":2}`, base.Add(time.Second))
	broken := f.save(t, "node/3", `{"v":3}`, base.Add(2*time.Second))

	raws := f.pending(t, cafe.ID, museum.ID, broken.ID, "00000000-0000-0000-0000-000000000000")
	if len(raws) != 3 || !raws[0].Latest || raws[0].Source != f.source || raws[0].ExternalID != "node/1" || string(raws[0].Payload) != `{"v":1}` {
		t.Fatalf("pending %+v", raws)
	}

	before := f.revision(t)
	outcome := materialize.Outcome{
		Apply: []materialize.Normalized{
			{Raw: raws[0], Place: draft("node/1", "gastro", "Кофейня", "gastro_coffee")},
			{Raw: raws[1], Place: draft("node/2", "culture", "Музей", "classical_art")},
		},
		Failed: []materialize.Rejected{{Raw: raws[2], Code: "missing_name"}},
	}
	revision, published, err := f.store.Publish(f.ctx, domain.Perm, outcome, base)
	if err != nil || !published || revision != before+1 || f.revision(t) != before+1 {
		t.Fatalf("publish revision %d published %v err %v, before %d", revision, published, err, before)
	}

	var categories []string
	var tagMask, poiRevision int64
	err = f.pool.QueryRow(f.ctx, `SELECT p.categories, p.tag_mask::bigint, p.catalog_revision FROM catalog.leisure_poi p
		WHERE p.city = 'perm' AND p.id = $1`, normalize.EntityID(string(f.source)+":node/1")).Scan(&categories, &tagMask, &poiRevision)
	if err != nil || len(categories) != 1 || categories[0] != "gastro" || tagMask != 1<<7 || poiRevision != revision {
		t.Fatalf("projection %v %b %d %v", categories, tagMask, poiRevision, err)
	}
	var deliveries int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM integration.change_delivery WHERE city = 'perm' AND catalog_revision = $1`, revision).Scan(&deliveries); err != nil || deliveries != 1 {
		t.Fatalf("deliveries %d, %v", deliveries, err)
	}
	if f.state(t, cafe.ID) != "applied" || f.state(t, broken.ID) != "failed" {
		t.Fatalf("states %s %s", f.state(t, cafe.ID), f.state(t, broken.ID))
	}
	if w := f.watermark(t); w == nil || !w.Equal(base.Add(time.Second)) {
		t.Fatalf("watermark %v stops before the failed record", w)
	}

	// A retried publish of the same batch finds nothing pending and changes nothing.
	again, published, err := f.store.Publish(f.ctx, domain.Perm, outcome, base)
	if err != nil || published || again != revision || f.revision(t) != revision {
		t.Fatalf("retry revision %d published %v err %v", again, published, err)
	}
	if raws := f.pending(t, cafe.ID); len(raws) != 0 {
		t.Fatalf("applied record still pending: %+v", raws)
	}

	// A new raw record with the accepted content is unchanged; an older one is superseded.
	older := f.save(t, "node/1", `{"v":"old"}`, base.Add(3*time.Second))
	same := f.save(t, "node/1", `{"v":1}`, base.Add(4*time.Second))
	raws = f.pending(t, older.ID, same.ID)
	if len(raws) != 2 || raws[0].Latest || !raws[1].Latest || string(raws[1].AcceptedHash) != string(raws[1].ContentHash) {
		t.Fatalf("pending %+v", raws)
	}
	revision, published, err = f.store.Publish(f.ctx, domain.Perm, materialize.Outcome{Superseded: raws[:1], Unchanged: raws[1:]}, base)
	if err != nil || published || f.revision(t) != again {
		t.Fatalf("unchanged batch revision %d published %v err %v", revision, published, err)
	}
	if f.state(t, older.ID) != "applied" || f.state(t, same.ID) != "applied" {
		t.Fatal("unchanged and superseded records stay pending")
	}

	// A late retry of the first batch must not bring back the title a newer version replaced.
	renamed := f.save(t, "node/1", `{"v":"renamed"}`, base.Add(6*time.Second))
	raws = f.pending(t, renamed.ID)
	if _, published, err = f.store.Publish(f.ctx, domain.Perm, materialize.Outcome{Apply: []materialize.Normalized{
		{Raw: raws[0], Place: draft("node/1", "gastro", "Новая кофейня", "gastro_coffee")}}}, base); err != nil || !published {
		t.Fatalf("rename published %v, err %v", published, err)
	}
	if _, _, err = f.store.Publish(f.ctx, domain.Perm, outcome, base); err != nil {
		t.Fatal(err)
	}
	var title string
	if err := f.pool.QueryRow(f.ctx, `SELECT title FROM catalog.place WHERE city = 'perm' AND id = $1`,
		normalize.EntityID(string(f.source)+":node/1")).Scan(&title); err != nil || title != "Новая кофейня" {
		t.Fatalf("title after a late retry %q, %v", title, err)
	}

	// The same content again leaves the place row as it is.
	reapply := f.save(t, "node/2", `{"v":"2b"}`, base.Add(5*time.Second))
	raws = f.pending(t, reapply.ID)
	_, published, err = f.store.Publish(f.ctx, domain.Perm, materialize.Outcome{Apply: []materialize.Normalized{
		{Raw: raws[0], Place: draft("node/2", "culture", "Музей", "classical_art")}}}, base)
	if err != nil || published {
		t.Fatalf("identical place published %v, err %v", published, err)
	}
}
