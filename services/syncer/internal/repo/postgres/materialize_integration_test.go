package postgres

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	gatewayv1 "github.com/andres1m/impuls-goroda/proto/gateway/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/andres1m/impuls-goroda/pkg/catalogevent"
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
	f := &materializeFixture{
		ctx:     ctx,
		pool:    pool,
		landing: NewLanding(pool),
		store:   NewMaterializeStore(func() *pgxpool.Pool { return pool }),
		source:  domain.SourceKey("test_" + randomSuffix(t)),
	}
	f.sourceID, err = f.landing.EnsureSource(
		ctx,
		&domain.Source{
			Key:           f.source,
			Name:          "Materialize test",
			AccessMode:    domain.AccessAPI,
			SchemaVersion: "1",
			DataMode:      domain.Live,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.cleanup(t) })
	return f
}

func (f *materializeFixture) cleanup(t *testing.T) {
	for _, q := range []string{
		`DELETE FROM integration.change_delivery d USING integration.source_record r WHERE d.source_record_id = r.id AND r.source_id = $1`,
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
	rec := domain.RawRecord{
		ExternalID:  externalID,
		SourceURL:   "https://example.test/" + externalID,
		Payload:     []byte(payload),
		ContentType: "application/json",
	}
	if _, err := f.landing.SaveRecord(f.ctx, f.sourceID, domain.Perm, domain.Live, &rec, at); err != nil {
		t.Fatal(err)
	}
	envelopes, err := f.landing.Unpublished(f.ctx, f.sourceID, domain.Perm)
	if err != nil {
		t.Fatal(err)
	}
	for i := range slices.Backward(envelopes) {
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
	if err := f.pool.QueryRow(f.ctx, `SELECT processing_state FROM integration.raw_ingest WHERE id = $1`, rawID).
		Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func (f *materializeFixture) watermark(t *testing.T) *time.Time {
	t.Helper()
	var at *time.Time
	err := f.pool.QueryRow(f.ctx, `SELECT (materialized_watermark->>'fetched_at')::timestamptz FROM integration.sync_cursor
		WHERE source_id = $1 AND city = 'perm'`, uuidParam(f.sourceID)).
		Scan(&at)
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
	if len(raws) != 3 || !raws[0].Latest || raws[0].Source != f.source || raws[0].ExternalID != "node/1" ||
		string(raws[0].Payload) != `{"v":1}` {
		t.Fatalf("pending %+v", raws)
	}

	outcome := &materialize.Outcome{
		Apply: []materialize.Normalized{
			{Raw: raws[0], Place: draft("node/1", "gastro", "Кофейня", "gastro_coffee")},
			{Raw: raws[1], Place: draft("node/2", "culture", "Музей", "classical_art")},
		},
		Failed: []materialize.Rejected{{Raw: raws[2], Code: "missing_name"}},
	}
	revision := f.assertInitialPublish(t, outcome, cafe.ID, broken.ID, base)
	f.assertRetryAndSuperseded(t, outcome, cafe.ID, revision, base)
	f.assertLateRetryAndIdentical(t, outcome, base)
}

func (f *materializeFixture) assertInitialPublish(
	t *testing.T,
	outcome *materialize.Outcome,
	cafeID, brokenID string,
	base time.Time,
) int64 {
	t.Helper()
	before := f.revision(t)
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
	if qErr := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM integration.change_delivery WHERE city = 'perm' AND catalog_revision = $1`, revision).
		Scan(&deliveries); qErr != nil ||
		deliveries != 1 {
		t.Fatalf("deliveries %d, %v", deliveries, qErr)
	}
	if f.state(t, cafeID) != "applied" || f.state(t, brokenID) != "failed" {
		t.Fatalf("states %s %s", f.state(t, cafeID), f.state(t, brokenID))
	}
	if w := f.watermark(t); w == nil || !w.Equal(base.Add(time.Second)) {
		t.Fatalf("watermark %v stops before the failed record", w)
	}
	return revision
}

func (f *materializeFixture) assertRetryAndSuperseded(
	t *testing.T,
	outcome *materialize.Outcome,
	cafeID string,
	revision int64,
	base time.Time,
) {
	t.Helper()
	again, published, err := f.store.Publish(f.ctx, domain.Perm, outcome, base)
	if err != nil || published || again != revision || f.revision(t) != revision {
		t.Fatalf("retry revision %d published %v err %v", again, published, err)
	}
	if pendingRaws := f.pending(t, cafeID); len(pendingRaws) != 0 {
		t.Fatalf("applied record still pending: %+v", pendingRaws)
	}

	older := f.save(t, "node/1", `{"v":"old"}`, base.Add(3*time.Second))
	same := f.save(t, "node/1", `{"v":1}`, base.Add(4*time.Second))
	raws := f.pending(t, older.ID, same.ID)
	if len(raws) != 2 || raws[0].Latest || !raws[1].Latest || !bytes.Equal(raws[1].AcceptedHash, raws[1].ContentHash) {
		t.Fatalf("pending %+v", raws)
	}
	rev, published, err := f.store.Publish(
		f.ctx,
		domain.Perm,
		&materialize.Outcome{Superseded: raws[:1], Unchanged: raws[1:]},
		base,
	)
	if err != nil || published || f.revision(t) != again {
		t.Fatalf("unchanged batch revision %d published %v err %v", rev, published, err)
	}
	if f.state(t, older.ID) != "applied" || f.state(t, same.ID) != "applied" {
		t.Fatal("unchanged and superseded records stay pending")
	}
}

func (f *materializeFixture) assertLateRetryAndIdentical(
	t *testing.T,
	outcome *materialize.Outcome,
	base time.Time,
) {
	t.Helper()
	renamed := f.save(t, "node/1", `{"v":"renamed"}`, base.Add(6*time.Second))
	raws := f.pending(t, renamed.ID)
	if _, published, err := f.store.Publish(f.ctx, domain.Perm, &materialize.Outcome{Apply: []materialize.Normalized{
		{
			Raw:   raws[0],
			Place: draft("node/1", "gastro", "Новая кофейня", "gastro_coffee"),
		},
	}}, base); err != nil || !published {
		t.Fatalf("rename published %v, err %v", published, err)
	}
	if _, _, err := f.store.Publish(f.ctx, domain.Perm, outcome, base); err != nil {
		t.Fatal(err)
	}
	var title string
	if err := f.pool.QueryRow(f.ctx, `SELECT title FROM catalog.place WHERE city = 'perm' AND id = $1`,
		normalize.EntityID(string(f.source)+":node/1")).Scan(&title); err != nil || title != "Новая кофейня" {
		t.Fatalf("title after a late retry %q, %v", title, err)
	}

	reapply := f.save(t, "node/2", `{"v":"2b"}`, base.Add(5*time.Second))
	raws = f.pending(t, reapply.ID)
	_, published, err := f.store.Publish(f.ctx, domain.Perm, &materialize.Outcome{Apply: []materialize.Normalized{
		{Raw: raws[0], Place: draft("node/2", "culture", "Музей", "classical_art")}}}, base)
	if err != nil || published {
		t.Fatalf("identical place published %v, err %v", published, err)
	}
}

func eventNormalized(raw *materialize.Raw, placeKey string, starts ...time.Time) materialize.Normalized {
	event := &normalize.EventDraft{ExternalID: "event:1@place:" + placeKey, Title: "Лекция", NormalizedTitle: "лекция",
		Category: "culture", Tags: []string{"lectures_workshops"}}
	for _, s := range starts {
		event.Sessions = append(event.Sessions, normalize.SessionDraft{StartsAt: s, EndsAt: s.Add(time.Hour),
			SlotType: "FIXED_SESSION", MinDuration: time.Hour, RecommendedDuration: time.Hour, AccessType: "ticket",
			Price: normalize.PriceDraft{Status: "fixed", AmountMin: new(int64(500)), AmountMax: new(int64(500))}})
	}
	return materialize.Normalized{Raw: *raw, Place: draft("place:"+placeKey, "", "Зал "+placeKey), Event: event}
}

func (f *materializeFixture) publishEvent(
	t *testing.T,
	version int,
	at time.Time,
	placeKey string,
	starts ...time.Time,
) bool {
	t.Helper()
	saved := f.save(t, "event:1", fmt.Sprintf(`{"v":%d}`, version), at.Add(time.Duration(version)*time.Second))
	raws := f.pending(t, saved.ID)
	_, published, err := f.store.Publish(f.ctx, domain.Perm,
		&materialize.Outcome{Apply: []materialize.Normalized{eventNormalized(&raws[0], placeKey, starts...)}}, at)
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
	if err := f.pool.QueryRow(f.ctx, `SELECT is_active FROM catalog.event WHERE city = 'perm' AND id = $1`, id).
		Scan(&active); err != nil {
		t.Fatal(err)
	}
	return active
}

func (f *materializeFixture) poiCategories(t *testing.T, placeID uuid.UUID) []string {
	t.Helper()
	var categories []string
	if err := f.pool.QueryRow(f.ctx, `SELECT categories FROM catalog.leisure_poi WHERE city = 'perm' AND id = $1`, placeID).
		Scan(&categories); err != nil {
		t.Fatal(err)
	}
	return categories
}

//nolint:cyclop // integration scenario verifies one atomic event lifecycle
func TestMaterializeEventsIntegration(t *testing.T) {
	f := newMaterializeFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	past, day1, day2 := now.Add(-3*time.Hour), now.Add(24*time.Hour), now.Add(48*time.Hour)
	placeA := normalize.EntityID(string(f.source) + ":place:a")
	placeB := normalize.EntityID(string(f.source) + ":place:b")
	eventA := normalize.EntityID(string(f.source) + ":event:1@place:a")
	eventB := normalize.EntityID(string(f.source) + ":event:1@place:b")
	pastA, day1A, day2A := normalize.SessionID(
		eventA,
		past,
	), normalize.SessionID(
		eventA,
		day1,
	), normalize.SessionID(
		eventA,
		day2,
	)

	before := f.revision(t)
	if !f.publishEvent(t, 1, now, "a", past, day1, day2) || f.revision(t) != before+1 {
		t.Fatal("first version did not publish one revision")
	}
	if got := f.poiCategories(t, placeA); !slices.Equal(got, []string{"culture"}) {
		t.Fatalf("projection categories %v", got)
	}
	var category *string
	if err := f.pool.QueryRow(f.ctx, `SELECT category FROM catalog.place WHERE city = 'perm' AND id = $1`, placeA).
		Scan(&category); err != nil ||
		category != nil {
		t.Fatalf("event venue category %v, %v", category, err)
	}
	if s := f.session(t, day2A); s.status != "unknown" || s.version != 1 || !s.priceActive {
		t.Fatalf("new session %+v", s)
	}

	revision := f.revision(t)
	if f.publishEvent(t, 2, now, "a", past, day1, day2) || f.revision(t) != revision {
		t.Fatal("the same content published a revision")
	}

	// The source drops the day-two and the past session: only the future one is withdrawn.
	if !f.publishEvent(t, 3, now, "a", day1) {
		t.Fatal("a withdrawn session did not publish")
	}
	if s := f.session(
		t,
		day2A,
	); s.status != "cancelled" || s.reason == nil || *s.reason != "source_removed" || s.version != 2 ||
		s.priceActive {
		t.Fatalf("withdrawn session %+v", s)
	}
	rows, err := f.pool.Query(f.ctx, `SELECT destination,payload FROM integration.change_delivery
		WHERE city='perm' AND catalog_revision=$1 AND event_type='catalog.lifecycle.v1'`, f.revision(t))
	if err != nil {
		t.Fatal(err)
	}
	var destinations []string
	for rows.Next() {
		var destination string
		var payload []byte
		if err := rows.Scan(&destination, &payload); err != nil {
			t.Fatal(err)
		}
		var event gatewayv1.DeliverCatalogLifecycleRequest
		decodeErr := protojson.Unmarshal(payload, &event)
		if decodeErr != nil || !bytes.Equal(event.SessionId, day2A[:]) || event.NewAvailabilityStatus != "cancelled" {
			t.Fatalf("lifecycle payload: %s, %v", payload, decodeErr)
		}
		destinations = append(destinations, destination)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	slices.Sort(destinations)
	if !slices.Equal(destinations, []string{"gateway", "kafka"}) {
		t.Fatalf("lifecycle destinations: %v", destinations)
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

	// Saved routes reference an event with its place and a session with its event, so a move must leave
	// those keys of the existing rows as they were.
	if !f.publishEvent(t, 5, now, "b", day1) {
		t.Fatal("a moved event did not publish")
	}
	var oldPlace, sessionEvent uuid.UUID
	if err := f.pool.QueryRow(f.ctx, `SELECT e.place_id, s.event_id FROM catalog.event e
		JOIN catalog.session s ON s.event_id = e.id AND s.city = e.city
		WHERE e.city = 'perm' AND e.id = $1 AND s.id = $2`, eventA, day2A).Scan(&oldPlace, &sessionEvent); err != nil ||
		oldPlace != placeA || sessionEvent != eventA {
		t.Fatalf("keys of the moved event rewritten: place %s, session event %s, %v", oldPlace, sessionEvent, err)
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
	if err := f.pool.QueryRow(f.ctx, `SELECT is_active FROM catalog.leisure_poi WHERE city = 'perm' AND id = $1`, placeA).
		Scan(&oldActive); err != nil ||
		oldActive {
		t.Fatalf("old place without events still searchable: %v, %v", oldActive, err)
	}
	if got := f.poiCategories(t, placeB); !slices.Equal(got, []string{"culture"}) {
		t.Fatalf("new place shows %v", got)
	}
}

func TestMaterializeExplicitSoldOutIntegration(t *testing.T) {
	f := newMaterializeFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(24 * time.Hour)
	for version := 1; version <= 2; version++ {
		raw := f.save(t, "event:1", fmt.Sprintf(`{"v":%d}`, version), now.Add(time.Duration(version)*time.Second))
		pending := f.pending(t, raw.ID)
		item := eventNormalized(&pending[0], "a", start)
		if version == 2 {
			item.Event.Sessions[0].AvailabilityStatus = "sold_out"
		}
		if _, published, err := f.store.Publish(f.ctx, domain.Perm,
			&materialize.Outcome{Apply: []materialize.Normalized{item}}, now); err != nil || !published {
			t.Fatalf("publish explicit status version %d: published=%v err=%v", version, published, err)
		}
	}
	sessionID := normalize.SessionID(normalize.EntityID(string(f.source)+":event:1@place:a"), start)
	if got := f.session(t, sessionID).status; got != "sold_out" {
		t.Fatalf("status = %q", got)
	}
	var observedAt *time.Time
	observedSQL := `SELECT availability_observed_at FROM catalog.session WHERE city='perm' AND id=$1`
	if err := f.pool.QueryRow(f.ctx, observedSQL, sessionID).Scan(&observedAt); err != nil || observedAt == nil {
		t.Fatalf("explicit availability observation = %v, err=%v", observedAt, err)
	}
	var count int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM integration.change_delivery
		WHERE city='perm' AND catalog_revision=$1 AND event_type='catalog.lifecycle.v1'`, f.revision(t)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("sold-out deliveries = %d, want gateway and kafka", count)
	}
	revision := f.revision(t)
	repeat := f.save(t, "event:1", `{"v":3}`, now.Add(3*time.Second))
	pending := f.pending(t, repeat.ID)
	item := eventNormalized(&pending[0], "a", start)
	item.Event.Sessions[0].AvailabilityStatus = "sold_out"
	outcome := &materialize.Outcome{Apply: []materialize.Normalized{item}}
	_, published, publishErr := f.store.Publish(f.ctx, domain.Perm, outcome, now)
	if publishErr != nil || published || f.revision(t) != revision {
		t.Fatalf("repeated sold-out status: published=%v err=%v", published, publishErr)
	}
}

func TestReopenIntegration(t *testing.T) {
	f := newMaterializeFixture(t)
	base := time.Now().UTC().Truncate(time.Second)
	older := f.save(t, "node/1", `{"v":1}`, base)
	latest := f.save(t, "node/1", `{"v":2}`, base.Add(time.Second))
	broken := f.save(t, "node/2", `{"v":3}`, base.Add(2*time.Second))
	waiting := f.save(t, "node/3", `{"v":4}`, base.Add(3*time.Second))
	raws := f.pending(t, older.ID, latest.ID, broken.ID)
	place := materialize.Normalized{Raw: raws[1], Place: draft("node/1", "gastro", "Кофейня", "gastro_coffee")}
	if _, _, err := f.store.Publish(f.ctx, domain.Perm, &materialize.Outcome{
		Superseded: raws[:1], Apply: []materialize.Normalized{place},
		Failed: []materialize.Rejected{{Raw: raws[2], Code: "missing_name"}},
	}, base); err != nil {
		t.Fatal(err)
	}

	ids, err := f.store.Reopen(f.ctx, f.source, domain.Perm)
	want := []string{latest.ID, waiting.ID}
	slices.Sort(want)
	if err != nil || !slices.Equal(ids, want) {
		t.Fatalf("reopened %v, want %v, err %v", ids, want, err)
	}
	if f.state(t, latest.ID) != "pending" || f.state(t, older.ID) != "applied" || f.state(t, broken.ID) != "failed" {
		t.Fatalf("states %s %s %s", f.state(t, latest.ID), f.state(t, older.ID), f.state(t, broken.ID))
	}
	again, err := f.store.Reopen(f.ctx, f.source, domain.Perm)
	if err != nil || !slices.Equal(again, want) {
		t.Fatalf("second reopen %v, %v", again, err)
	}

	// The reopened record is no longer taken as unchanged, yet publishing the same content changes nothing.
	reopened := f.pending(t, latest.ID)
	if len(reopened) != 1 || reopened[0].AcceptedHash != nil {
		t.Fatalf("reopened record %+v", reopened)
	}
	revision := f.revision(t)
	place.Raw = reopened[0]
	if _, published, err := f.store.Publish(
		f.ctx,
		domain.Perm,
		&materialize.Outcome{Apply: []materialize.Normalized{place}},
		base,
	); err != nil || published ||
		f.revision(t) != revision {
		t.Fatalf("republish published %v, err %v", published, err)
	}
	if f.state(t, latest.ID) != "applied" {
		t.Fatal("republished record stays pending")
	}
}

func TestReopenKeepsSessionVersionsIntegration(t *testing.T) {
	f := newMaterializeFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	day1 := now.Add(24 * time.Hour)
	f.publishEvent(t, 1, now, "a", day1)
	ids, err := f.store.Reopen(f.ctx, f.source, domain.Perm)
	if err != nil || len(ids) != 1 {
		t.Fatalf("reopened %v, %v", ids, err)
	}
	raws := f.pending(t, ids...)
	revision := f.revision(t)
	if _, published, err := f.store.Publish(
		f.ctx,
		domain.Perm,
		&materialize.Outcome{
			Apply: []materialize.Normalized{eventNormalized(&raws[0], "a", day1)},
		},
		now,
	); err != nil || published {
		t.Fatalf("republish published %v, err %v", published, err)
	}
	session := normalize.SessionID(normalize.EntityID(string(f.source)+":event:1@place:a"), day1)
	if s := f.session(t, session); s.version != 1 || f.revision(t) != revision {
		t.Fatalf("session %+v, revision %d → %d", s, revision, f.revision(t))
	}
}

// Events of one source may describe their shared place slightly differently; republishing them must not
// count the place as changed just because the batch writes it once per event.
func TestSharedPlaceDoesNotFlapIntegration(t *testing.T) {
	f := newMaterializeFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	batch := func(version int) *materialize.Outcome {
		first := f.save(
			t,
			"event:1",
			fmt.Sprintf(`{"v":%d,"e":1}`, version),
			now.Add(time.Duration(2*version)*time.Second),
		)
		second := f.save(
			t,
			"event:2",
			fmt.Sprintf(`{"v":%d,"e":2}`, version),
			now.Add(time.Duration(2*version+1)*time.Second),
		)
		raws := f.pending(t, first.ID, second.ID)
		a := eventNormalized(&raws[0], "a", now.Add(24*time.Hour))
		b := eventNormalized(&raws[1], "a", now.Add(48*time.Hour))
		b.Event.ExternalID = "event:2@place:a"
		a.Place.Address, b.Place.Address = new("ул Радио, д 17"), new("ул Радио,д 17")
		return &materialize.Outcome{Apply: []materialize.Normalized{a, b}}
	}
	if _, published, err := f.store.Publish(f.ctx, domain.Perm, batch(1), now); err != nil || !published {
		t.Fatalf("first batch published %v, err %v", published, err)
	}
	revision := f.revision(t)
	if _, published, err := f.store.Publish(
		f.ctx,
		domain.Perm,
		batch(2),
		now,
	); err != nil || published ||
		f.revision(t) != revision {
		t.Fatalf("same batch again published %v, err %v, revision %d → %d", published, err, revision, f.revision(t))
	}
	var address string
	if err := f.pool.QueryRow(f.ctx, `SELECT address_text FROM catalog.place WHERE city = 'perm' AND id = $1`,
		normalize.EntityID(string(f.source)+":place:a")).Scan(&address); err != nil || address != "ул Радио,д 17" {
		t.Fatalf("place keeps %q, %v; the last description in the batch wins", address, err)
	}
}

func (f *materializeFixture) announcement(t *testing.T, revision int64) catalogevent.Invalidation {
	t.Helper()
	var payload []byte
	if err := f.pool.QueryRow(f.ctx, `SELECT payload FROM integration.change_delivery
		WHERE city = 'perm' AND destination = 'redis' AND catalog_revision = $1`, revision).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	m, err := catalogevent.Decode(payload)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestUrgentChangesAreAnnouncedToCachesIntegration(t *testing.T) {
	f := newMaterializeFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	day1, day2 := now.Add(24*time.Hour), now.Add(48*time.Hour)
	eventA := normalize.EntityID(string(f.source) + ":event:1@place:a")
	day2A := normalize.SessionID(eventA, day2)

	f.publishEvent(t, 1, now, "a", day1, day2)
	if m := f.announcement(t, f.revision(t)); m.Reason != catalogevent.ReasonIngest || len(m.Sessions) != 0 {
		t.Fatalf("a batch without urgent changes announced %+v", m)
	}

	recorded := testutil.ToFloat64(lifecycleEvents)
	if !f.publishEvent(t, 2, now, "a", day1) {
		t.Fatal("a withdrawn session did not publish")
	}
	if m := f.announcement(t, f.revision(t)); m.Reason != catalogevent.ReasonUrgent ||
		!slices.Equal(m.Sessions, []string{day2A.String()}) {
		t.Fatalf("a cancellation announced %+v", m)
	}
	if got := testutil.ToFloat64(lifecycleEvents) - recorded; got != 1 {
		t.Fatalf("recorded %v urgent changes, want 1", got)
	}
}
