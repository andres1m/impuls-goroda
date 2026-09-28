package postgres

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

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
		`DELETE FROM catalog.price_offer o USING integration.source_record r WHERE o.source_record_id = r.id AND r.source_id = $1`,
		`DELETE FROM catalog.session s USING integration.source_record r WHERE s.card_source_record_id = r.id AND r.source_id = $1`,
		`DELETE FROM catalog.event e USING integration.source_record r WHERE e.card_source_record_id = r.id AND r.source_id = $1`,
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

func ptr[T any](v T) *T { return &v }

func eventNormalized(raw materialize.Raw, placeKey string, starts ...time.Time) materialize.Normalized {
	event := &normalize.EventDraft{ExternalID: "event:1@place:" + placeKey, Title: "Лекция", NormalizedTitle: "лекция",
		Category: "culture", Tags: []string{"lectures_workshops"}}
	for _, s := range starts {
		event.Sessions = append(event.Sessions, normalize.SessionDraft{StartsAt: s, EndsAt: s.Add(time.Hour),
			SlotType: "FIXED_SESSION", MinDuration: time.Hour, RecommendedDuration: time.Hour, AccessType: "ticket",
			Price: normalize.PriceDraft{Status: "fixed", AmountMin: ptr(int64(500)), AmountMax: ptr(int64(500))}})
	}
	return materialize.Normalized{Raw: raw, Place: draft("place:"+placeKey, "", "Зал "+placeKey), Event: event}
}

func (f *materializeFixture) publishEvent(t *testing.T, version int, at time.Time, placeKey string, starts ...time.Time) bool {
	t.Helper()
	saved := f.save(t, "event:1", fmt.Sprintf(`{"v":%d}`, version), at.Add(time.Duration(version)*time.Second))
	raws := f.pending(t, saved.ID)
	_, published, err := f.store.Publish(f.ctx, domain.Perm,
		materialize.Outcome{Apply: []materialize.Normalized{eventNormalized(raws[0], placeKey, starts...)}}, at)
	if err != nil {
		t.Fatalf("publish version %d: %v", version, err)
	}
	return published
}

type materializedSession struct {
	status      string
	reason      *string
	version     int64
	priceActive bool
}

func (f *materializeFixture) session(t *testing.T, id uuid.UUID) materializedSession {
	t.Helper()
	var s materializedSession
	err := f.pool.QueryRow(f.ctx, `SELECT s.availability_status, s.cancellation_reason, s.version, o.is_active
		FROM catalog.session s JOIN catalog.price_offer o ON o.session_id = s.id AND o.city = s.city
		WHERE s.city = 'perm' AND s.id = $1`, id).Scan(&s.status, &s.reason, &s.version, &s.priceActive)
	if err != nil {
		t.Fatalf("session %s: %v", id, err)
	}
	return s
}

func (f *materializeFixture) eventActive(t *testing.T, id uuid.UUID) bool {
	t.Helper()
	var active bool
	if err := f.pool.QueryRow(f.ctx, `SELECT is_active FROM catalog.event WHERE city = 'perm' AND id = $1`, id).Scan(&active); err != nil {
		t.Fatal(err)
	}
	return active
}

func (f *materializeFixture) poiCategories(t *testing.T, placeID uuid.UUID) []string {
	t.Helper()
	var categories []string
	if err := f.pool.QueryRow(f.ctx, `SELECT categories FROM catalog.leisure_poi WHERE city = 'perm' AND id = $1`, placeID).Scan(&categories); err != nil {
		t.Fatal(err)
	}
	return categories
}

// reference saves a route that visits the session, the way the gateway does.
func (f *materializeFixture) reference(t *testing.T, placeID, eventID, sessionID, priceID uuid.UUID) {
	t.Helper()
	user, route := uuid.New(), uuid.New()
	// A route and its current revision reference each other, so they go in together.
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx) //nolint:errcheck // a no-op after commit
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO identity.user_account (id, max_user_id, created_at, last_seen_at, account_state, account_kind)
			VALUES ($1, $2, now(), now(), 'active', 'test')`, []any{user, "test:" + user.String()}},
		{`INSERT INTO planning.route (id, owner_id, city, lifecycle_state, current_revision, created_at, updated_at)
			VALUES ($1, $2, 'perm', 'saved', 1, now(), now())`, []any{route, user}},
		{`INSERT INTO planning.route_revision (route_id, revision, lifecycle_state, archetype_id, timezone, start_at, end_at,
				origin, input_schema_version, constraints, catalog_revision, result_status, warnings, cost_summary,
				mutation_kind, created_at)
			VALUES ($1, 1, 'saved', 'history_heritage', 'Asia/Yekaterinburg', now(), now() + interval '8 hours',
				ST_SetSRID(ST_MakePoint(56.25, 58.01), 4326), 1, '{}', 0, 'ok', '[]', '{}', 'create', now())`, []any{route}},
		{`INSERT INTO planning.route_visit (route_id, visit_id, visit_kind, city, place_id, event_id, session_id,
				price_offer_id, created_in_revision, created_at)
			VALUES ($1, $2, 'visit', 'perm', $3, $4, $5, $6, 1, now())`, []any{route, uuid.New(), placeID, eventID, sessionID, priceID}},
	} {
		if _, err := tx.Exec(f.ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM planning.route WHERE id = $1`, `DELETE FROM identity.user_account WHERE id = $1`} {
			id := route
			if strings.Contains(q, "user_account") {
				id = user
			}
			if _, err := f.pool.Exec(context.Background(), q, id); err != nil {
				t.Errorf("cleanup: %v", err)
			}
		}
	})
}

func TestMaterializeEventsIntegration(t *testing.T) {
	f := newMaterializeFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	past, day1, day2 := now.Add(-3*time.Hour), now.Add(24*time.Hour), now.Add(48*time.Hour)
	placeA := normalize.EntityID(string(f.source) + ":place:a")
	placeB := normalize.EntityID(string(f.source) + ":place:b")
	eventA := normalize.EntityID(string(f.source) + ":event:1@place:a")
	eventB := normalize.EntityID(string(f.source) + ":event:1@place:b")
	pastA, day1A, day2A := normalize.SessionID(eventA, past), normalize.SessionID(eventA, day1), normalize.SessionID(eventA, day2)

	before := f.revision(t)
	if !f.publishEvent(t, 1, now, "a", past, day1, day2) || f.revision(t) != before+1 {
		t.Fatal("first version did not publish one revision")
	}
	if got := f.poiCategories(t, placeA); !slices.Equal(got, []string{"culture"}) {
		t.Fatalf("projection categories %v", got)
	}
	var category *string
	if err := f.pool.QueryRow(f.ctx, `SELECT category FROM catalog.place WHERE city = 'perm' AND id = $1`, placeA).Scan(&category); err != nil || category != nil {
		t.Fatalf("event venue category %v, %v", category, err)
	}
	if s := f.session(t, day2A); s.status != "unknown" || s.version != 1 || !s.priceActive {
		t.Fatalf("new session %+v", s)
	}
	f.reference(t, placeA, eventA, day2A, normalize.PriceID(day2A))

	revision := f.revision(t)
	if f.publishEvent(t, 2, now, "a", past, day1, day2) || f.revision(t) != revision {
		t.Fatal("the same content published a revision")
	}

	// The source drops the day-two and the past session: only the future one is withdrawn.
	if !f.publishEvent(t, 3, now, "a", day1) {
		t.Fatal("a withdrawn session did not publish")
	}
	if s := f.session(t, day2A); s.status != "cancelled" || s.reason == nil || *s.reason != "source_removed" || s.version != 2 || s.priceActive {
		t.Fatalf("withdrawn session %+v", s)
	}
	if s := f.session(t, pastA); s.status != "unknown" || s.version != 1 {
		t.Fatalf("past session touched %+v", s)
	}

	if !f.publishEvent(t, 4, now, "a", day1, day2) {
		t.Fatal("a returning session did not publish")
	}
	if s := f.session(t, day2A); s.status != "unknown" || s.reason != nil || s.version != 3 || !s.priceActive {
		t.Fatalf("returned session %+v", s)
	}

	// The event moves while a saved route still points at it and its session.
	if !f.publishEvent(t, 5, now, "b", day1) {
		t.Fatal("a moved event did not publish")
	}
	if f.eventActive(t, eventA) || !f.eventActive(t, eventB) {
		t.Fatal("the move did not replace the event")
	}
	for _, id := range []uuid.UUID{day1A, day2A} {
		if s := f.session(t, id); s.status != "cancelled" {
			t.Fatalf("session of the moved event %+v", s)
		}
	}
	if s := f.session(t, normalize.SessionID(eventB, day1)); s.status != "unknown" {
		t.Fatalf("session at the new place %+v", s)
	}
	var oldActive bool
	if err := f.pool.QueryRow(f.ctx, `SELECT is_active FROM catalog.leisure_poi WHERE city = 'perm' AND id = $1`, placeA).Scan(&oldActive); err != nil || oldActive {
		t.Fatalf("old place without events still searchable: %v, %v", oldActive, err)
	}
	if got := f.poiCategories(t, placeB); !slices.Equal(got, []string{"culture"}) {
		t.Fatalf("new place shows %v", got)
	}
}
