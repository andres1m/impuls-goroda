package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/andres1m/impuls-goroda/pkg/ai"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/materialize"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/normalize"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/resolve"
)

type stubEmbedder struct {
	vectors func(text string) []float32
	err     error
	calls   int
}

func (e *stubEmbedder) Embed(_ context.Context, texts []string) (ai.Embedding, error) {
	e.calls++
	if e.err != nil {
		return ai.Embedding{}, e.err
	}
	out := ai.Embedding{Space: ai.Space{Key: "stub", Version: "d2"}}
	for _, s := range texts {
		out.Vectors = append(out.Vectors, e.vectors(s))
	}
	return out, nil
}
func (e *stubEmbedder) Space() ai.Space { return ai.Space{Key: "stub", Version: "d2"} }

func identical(string) []float32 { return []float32{1, 0} }

// pair returns two fixtures that share one embedder and one trust order in which `first` outranks
// `second` for every attribute but the coordinates.
func pair(t *testing.T, e *stubEmbedder) (first, second *materializeFixture) {
	t.Helper()
	first, second = newMaterializeFixture(t), newMaterializeFixture(t)
	trust := resolve.Trust{}
	for _, a := range []string{resolve.AttrTitle, resolve.AttrAddress, resolve.AttrCategory, resolve.AttrTags, resolve.AttrOpeningRules} {
		trust[a] = []domain.SourceKey{first.source, second.source}
	}
	trust[resolve.AttrCoordinates] = []domain.SourceKey{second.source, first.source}
	for _, f := range []*materializeFixture{first, second} {
		f.store.trust = trust
		f.store.embedder = e
	}
	t.Cleanup(func() { wipeCatalog(t, first.pool, first.sourceID, second.sourceID) })
	return first, second
}

func sourceIDs(fs ...*materializeFixture) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = uuid.UUID(f.sourceID).String()
	}
	return out
}

var stamp = time.Now().UTC().Truncate(time.Microsecond)

// arrive lands one place record and runs it through resolution and publication.
func (f *materializeFixture) arrive(t *testing.T, at time.Duration, d normalize.PlaceDraft) materialize.Normalized {
	t.Helper()
	payload := fmt.Sprintf(`{"key":%q,"title":%q,"at":%d}`, d.ExternalID, d.Title, at)
	raw := f.save(t, d.ExternalID, payload, stamp.Add(at))
	o := &materialize.Outcome{Apply: []materialize.Normalized{{Raw: f.pending(t, raw.ID)[0], Place: d}}}
	if err := f.store.Resolve(f.ctx, domain.Perm, o); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.store.Publish(f.ctx, domain.Perm, o, stamp.Add(at)); err != nil {
		t.Fatal(err)
	}
	return o.Apply[0]
}

type placeRow struct {
	Title, Address, Category string
	Lat, Lon                 float64
	Mask                     int64
	Review                   bool
	Mode                     string
}

func (f *materializeFixture) row(t *testing.T, id uuid.UUID) placeRow {
	t.Helper()
	var r placeRow
	var address, category *string
	err := f.pool.QueryRow(f.ctx, `SELECT title, address_text, category, ST_Y(coordinates), ST_X(coordinates),
		tag_mask::bigint, review_required, data_mode FROM catalog.place WHERE id = $1 AND city = 'perm'`, id).
		Scan(&r.Title, &address, &category, &r.Lat, &r.Lon, &r.Mask, &r.Review, &r.Mode)
	if err != nil {
		t.Fatal(err)
	}
	if address != nil {
		r.Address = *address
	}
	if category != nil {
		r.Category = *category
	}
	return r
}

func (f *materializeFixture) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(f.ctx, sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSourcesOfOneVenueShareAPlaceIntegration(t *testing.T) {
	e := &stubEmbedder{vectors: identical}
	first, second := pair(t, e)
	a := first.arrive(t, 0, draftAt("n1", "gastro", "Кофейня Центральная", testLat, testLon, "gastro_coffee"))
	addr := "ул. Ленина, 1"
	dB := draftAt("k1", "gastro", "ГБУК Кофейня Центральная", testLat, testLon+metersEast(2))
	dB.Address = &addr
	revision := first.revision(t)
	b := second.arrive(t, time.Second, dB)

	if a.Resolution.Kind != resolve.Own || b.Resolution.Kind != resolve.Merge || b.Resolution.PlaceID != ownPlaceID(&a) {
		t.Fatalf("resolutions %+v / %+v", a.Resolution, b.Resolution)
	}
	if first.revision(t) != revision+1 {
		t.Fatal("the merge changed the place and must publish one revision")
	}
	row := first.row(t, ownPlaceID(&a))
	// The first source outranks the second for the name and tags; the second one for the coordinates; the
	// address is only known to the second.
	if row.Title != "Кофейня Центральная" || row.Address != addr || row.Lat != testLat ||
		math.Abs(row.Lon-(testLon+metersEast(2))) > 1e-9 || row.Mask != 1<<7 || row.Mode != "live" || row.Review {
		t.Fatalf("merged place %+v", row)
	}
	if n := first.count(t, `SELECT count(*) FROM catalog.place p JOIN integration.source_record r ON r.id = p.card_source_record_id
		WHERE r.source_id = ANY($1::uuid[]) AND p.title LIKE '%Кофейня Центральная'`, sourceIDs(first, second)); n != 1 {
		t.Fatalf("%d place rows for one venue", n)
	}
	if n := first.count(t, `SELECT count(*) FROM integration.entity_link WHERE place_id = $1 AND city = 'perm'`, ownPlaceID(&a)); n != 2 {
		t.Fatalf("%d links", n)
	}
	if n := first.count(t, `SELECT count(*) FROM integration.entity_link WHERE place_id = $1 AND match_score >= 0.85`, ownPlaceID(&a)); n != 1 {
		t.Fatalf("the merged link must carry its score, found %d", n)
	}
	for _, attr := range []string{"title", "coordinates", "address", "category", "tags", "opening_rules"} {
		want := 1
		if attr == "opening_rules" {
			want = 0
		}
		if n := first.count(t, `SELECT count(*) FROM integration.attribute_fact WHERE target_id = $1 AND attribute_name = $2 AND is_selected`,
			ownPlaceID(&a), attr); n != want {
			t.Fatalf("%s: %d selected facts, want %d", attr, n, want)
		}
	}

	revision = first.revision(t)
	again := second.arrive(t, 2*time.Second, dB)
	if again.Resolution.Kind != resolve.Merge || !again.Resolution.Known || first.revision(t) != revision {
		t.Fatalf("a repeated arrival changed something: %+v revision %d -> %d", again.Resolution, revision, first.revision(t))
	}
	if n := first.count(t, `SELECT count(*) FROM integration.entity_link WHERE place_id = $1`, ownPlaceID(&a)); n != 2 {
		t.Fatalf("%d links after a repeat", n)
	}
}

func TestArrivalOrderDoesNotChangeTheValuesIntegration(t *testing.T) {
	// dx moves one run away from the other so that their places never see each other as candidates.
	values := func(firstOrder bool, dx float64) placeRow {
		e := &stubEmbedder{vectors: identical}
		first, second := pair(t, e)
		addr := "ул. Ленина, 1"
		dA := draftAt("n1", "gastro", "Кофейня Центральная", testLat, testLon+dx, "gastro_coffee")
		dB := draftAt("k1", "gastro", "ГБУК Кофейня Центральная", testLat, testLon+dx+metersEast(2))
		dB.Address = &addr
		var host materialize.Normalized
		if firstOrder {
			host = first.arrive(t, 0, dA)
			second.arrive(t, time.Second, dB)
		} else {
			host = second.arrive(t, 0, dB)
			first.arrive(t, time.Second, dA)
		}
		return first.row(t, ownPlaceID(&host))
	}
	a, b := values(true, 0), values(false, 0.01)
	a.Lon, b.Lon = 0, 0 // the runs sit at different places by design; everything else must agree
	if a != b {
		t.Fatalf("order changes the place: %+v vs %+v", a, b)
	}
}

func TestNeighbouringVenuesStayApartIntegration(t *testing.T) {
	e := &stubEmbedder{vectors: identical}
	first, second := pair(t, e)
	a := first.arrive(t, 0, draftAt("n1", "culture", "Музей МХАТ", testLat, testLon))
	b := second.arrive(t, time.Second, draftAt("k1", "culture", "Музей С. С. Прокофьева", testLat, testLon+metersEast(45)))
	if b.Resolution.Kind != resolve.Own || e.calls != 0 || ownPlaceID(&a) == ownPlaceID(&b) {
		t.Fatalf("resolution %+v, embedder calls %d", b.Resolution, e.calls)
	}
}

func TestPlacesOfOneSourceAreNeverMergedIntegration(t *testing.T) {
	first, _ := pair(t, &stubEmbedder{vectors: identical})
	first.arrive(t, 0, draftAt("n1", "gastro", "Йоши Тоши", testLat, testLon))
	b := first.arrive(t, time.Second, draftAt("n2", "gastro", "Йоши тоши", testLat, testLon+metersEast(9)))
	if b.Resolution.Kind != resolve.Own {
		t.Fatalf("resolution %+v", b.Resolution)
	}
}

func TestAmbiguousArrivalIsFlaggedIntegration(t *testing.T) {
	first, second := pair(t, &stubEmbedder{vectors: identical})
	first.arrive(t, 0, draftAt("n1", "gastro", "Кофейня Центральная", testLat, testLon))
	first.arrive(t, time.Second, draftAt("n2", "gastro", "Кофейня Центральная", testLat, testLon+metersEast(6)))
	b := second.arrive(t, 2*time.Second, draftAt("k1", "gastro", "Кофейня Центральная", testLat, testLon+metersEast(3)))
	if b.Resolution.Kind != resolve.Review || b.Resolution.Reason != resolve.ReasonAmbiguous {
		t.Fatalf("resolution %+v", b.Resolution)
	}
	if !second.row(t, ownPlaceID(&b)).Review {
		t.Fatal("the separate place is not marked for review")
	}
	if n := second.count(t, `SELECT count(*) FROM integration.entity_link WHERE place_id = $1 AND resolution_status = 'review_required'`,
		ownPlaceID(&b)); n != 1 {
		t.Fatalf("%d review links", n)
	}
}

func TestArrivalWithoutAVectorIsFlaggedIntegration(t *testing.T) {
	e := &stubEmbedder{err: errors.New("no key")}
	first, second := pair(t, e)
	first.arrive(t, 0, draftAt("n1", "gastro", "Кофейня Центральная", testLat, testLon))
	b := second.arrive(t, time.Second, draftAt("k1", "gastro", "Кофейня Центральная", testLat, testLon+metersEast(3)))
	if b.Resolution.Kind != resolve.Review || b.Resolution.Reason != resolve.ReasonNoVector || !second.row(t, ownPlaceID(&b)).Review {
		t.Fatalf("resolution %+v", b.Resolution)
	}
}

func TestOneBatchWithTwoSourcesMergesIntegration(t *testing.T) {
	first, second := pair(t, &stubEmbedder{vectors: identical})
	rawA := first.save(t, "n1", `{"v":1}`, stamp)
	rawB := second.save(t, "k1", `{"v":1}`, stamp.Add(time.Second))
	o := &materialize.Outcome{Apply: []materialize.Normalized{
		{Raw: first.pending(t, rawA.ID)[0], Place: draftAt("n1", "gastro", "Кофейня Центральная", testLat, testLon)},
		{Raw: second.pending(t, rawB.ID)[0], Place: draftAt("k1", "gastro", "Кофейня Центральная", testLat, testLon+metersEast(2))},
	}}
	if err := first.store.Resolve(first.ctx, domain.Perm, o); err != nil {
		t.Fatal(err)
	}
	if _, _, err := first.store.Publish(first.ctx, domain.Perm, o, stamp.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if o.Apply[1].Resolution.Kind != resolve.Merge || o.Apply[1].Resolution.PlaceID != ownPlaceID(&o.Apply[0]) {
		t.Fatalf("resolutions %+v", o.Apply)
	}
	if n := first.count(t, `SELECT count(*) FROM integration.entity_link WHERE place_id = $1`, ownPlaceID(&o.Apply[0])); n != 2 {
		t.Fatalf("%d links", n)
	}
}

func TestEventOfAMergedRecordAttachesToTheSharedPlaceIntegration(t *testing.T) {
	first, second := pair(t, &stubEmbedder{vectors: identical})
	a := first.arrive(t, 0, draftAt("n1", "culture", "Дом музыки", testLat, testLon))
	start := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Hour)

	raw := second.save(t, "event:1@place:dm", `{"v":1}`, stamp.Add(time.Second))
	n := materialize.Normalized{Raw: second.pending(t, raw.ID)[0],
		Place: draftAt("place:dm", "", "Дом музыки", testLat, testLon+metersEast(3)),
		Event: &normalize.EventDraft{ExternalID: "event:1@place:dm", Title: "Концерт", NormalizedTitle: "концерт", Category: "culture",
			Sessions: []normalize.SessionDraft{{StartsAt: start, EndsAt: start.Add(time.Hour), SlotType: "FIXED_SESSION",
				MinDuration: time.Hour, RecommendedDuration: time.Hour, AccessType: "ticket",
				Price: normalize.PriceDraft{Status: "fixed", AmountMin: new(int64(500)), AmountMax: new(int64(500))}}}}}
	o := &materialize.Outcome{Apply: []materialize.Normalized{n}}
	if err := second.store.Resolve(second.ctx, domain.Perm, o); err != nil {
		t.Fatal(err)
	}
	if _, _, err := second.store.Publish(second.ctx, domain.Perm, o, stamp.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if o.Apply[0].Resolution.Kind != resolve.Merge {
		t.Fatalf("resolution %+v", o.Apply[0].Resolution)
	}
	eventID := normalize.EntityID(string(second.source) + ":event:1@place:dm")
	if c := first.count(t, `SELECT count(*) FROM catalog.event WHERE id = $1 AND place_id = $2 AND city = 'perm'`, eventID, ownPlaceID(&a)); c != 1 {
		t.Fatal("the event is not at the shared place")
	}
	if c := first.count(t, `SELECT count(*) FROM catalog.place p JOIN integration.source_record r ON r.id = p.card_source_record_id
		WHERE r.source_id = ANY($1::uuid[]) AND p.title = 'Дом музыки'`, sourceIDs(first, second)); c != 1 {
		t.Fatal("a second place row was created for the venue")
	}
}

func TestSingleSourceReprocessingKeepsTheCatalogIntegration(t *testing.T) {
	f := newMaterializeFixture(t)
	f.store.embedder = &stubEmbedder{vectors: identical}
	d := draftAt("n1", "gastro", "Кофейня Центральная", testLat, testLon, "gastro_coffee")
	a := f.arrive(t, 0, d)
	before, revision := f.row(t, ownPlaceID(&a)), f.revision(t)
	f.arrive(t, time.Second, d)
	if f.row(t, ownPlaceID(&a)) != before || f.revision(t) != revision {
		t.Fatal("processing the same place again changed the catalog")
	}
	if n := f.count(t, `SELECT count(*) FROM integration.attribute_fact WHERE target_id = $1 AND attribute_name = 'title'`, ownPlaceID(&a)); n != 1 {
		t.Fatalf("%d title facts after an identical record", n)
	}
	d.Title, d.NormalizedTitle = "Кофейня на Ленина", "кофейня на ленина"
	f.arrive(t, 2*time.Second, d)
	if f.row(t, ownPlaceID(&a)).Title != "Кофейня на Ленина" || f.revision(t) != revision+1 {
		t.Fatal("a changed record did not update its place")
	}
}

func TestKnownAndExistingIntegration(t *testing.T) {
	first, second := pair(t, &stubEmbedder{vectors: identical})
	a := first.arrive(t, 0, draftAt("n1", "gastro", "Кофейня Центральная", testLat, testLon))
	second.arrive(t, time.Second, draftAt("k1", "gastro", "Кофейня Центральная", testLat, testLon+metersEast(2)))

	data := placeData{pool: first.pool}
	known, err := data.Known(first.ctx, domain.Perm, second.source, []string{"k1", "absent"})
	if err != nil || len(known) != 1 || known["k1"] != ownPlaceID(&a) {
		t.Fatalf("known %v %v", known, err)
	}
	existing, err := data.Existing(first.ctx, domain.Perm, []uuid.UUID{ownPlaceID(&a), uuid.New()})
	if err != nil || len(existing) != 1 || !existing[ownPlaceID(&a)] {
		t.Fatalf("existing %v %v", existing, err)
	}
}
