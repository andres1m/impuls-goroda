package materialize

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
)

const cafe = `{"type":"node","id":1,"lat":58.01,"lon":56.25,"tags":{"name":"Кофейня","amenity":"cafe"}}`

func raw(id, external, payload string) Raw {
	return Raw{
		ID:             id,
		SourceRecordID: "rec-" + external,
		Source:         domain.OSM,
		ExternalID:     external,
		Payload:        []byte(payload),
		ContentHash: []byte(
			id,
		),
		FetchedAt: time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC),
		DataMode:  domain.Live,
		Latest:    true,
	}
}

func ids(rs []Raw) []string {
	var out []string
	for i := range rs {
		out = append(out, rs[i].ID)
	}
	return out
}

func TestPrepareSortsTheBatch(t *testing.T) {
	older := raw("r1", "node/1", cafe)
	older.Latest = false
	unchanged := raw("r2", "node/2", cafe)
	unchanged.AcceptedHash = []byte("r2")
	unknown := raw("r3", "event:5", `{}`)
	unknown.Source = domain.SyntheticSource
	broken := raw("r4", "node/4", `{"type":"node","id":4,"lat":58,"lon":56,"tags":{"amenity":"cafe"}}`)
	fresh := raw("r5", "node/1", cafe)
	changed := raw("r6", "node/6", cafe)
	changed.AcceptedHash = []byte("before")

	o, deferred := Prepare(domain.Perm, []Raw{older, unchanged, unknown, broken, fresh, changed}, time.Now())

	if got := ids(deferred); len(got) != 1 || got[0] != "r3" {
		t.Fatalf("deferred %v", got)
	}
	if got := ids(o.Superseded); len(got) != 1 || got[0] != "r1" {
		t.Fatalf("superseded %v", got)
	}
	if got := ids(o.Unchanged); len(got) != 1 || got[0] != "r2" {
		t.Fatalf("unchanged %v", got)
	}
	if len(o.Failed) != 1 || o.Failed[0].Raw.ID != "r4" || o.Failed[0].Code != "missing_name" {
		t.Fatalf("failed %+v", o.Failed)
	}
	if len(o.Apply) != 2 || o.Apply[0].Raw.ID != "r5" || o.Apply[1].Raw.ID != "r6" ||
		o.Apply[0].Place.Category != "gastro" {
		t.Fatalf("apply %+v", o.Apply)
	}
}

type fakeStore struct {
	pending   []Raw
	published []Outcome
	revision  int64
	err       error
	outside   []bool
	known     bool
	points    []Point
}

func (f *fakeStore) OutsideBoundary(
	_ context.Context,
	_ domain.City,
	points []Point,
) (outside []bool, known bool, err error) {
	f.points = points
	return f.outside, f.known, nil
}

func (f *fakeStore) PendingBatch(context.Context, domain.City, []string) ([]Raw, error) {
	return f.pending, f.err
}

func (f *fakeStore) Publish(
	_ context.Context,
	_ domain.City,
	o *Outcome,
	_ time.Time,
) (revision int64, published bool, err error) {
	f.published = append(f.published, *o)
	return f.revision, len(o.Apply) > 0, nil
}

func TestApplyPublishesThePreparedBatch(t *testing.T) {
	s := &fakeStore{pending: []Raw{raw("r1", "node/1", cafe)}, revision: 12}
	res, err := Apply(context.Background(), s, domain.Perm, []string{"r1"}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.published) != 1 || res.Applied != 1 || res.CatalogRevision != 12 {
		t.Fatalf("result %+v, published %d", res, len(s.published))
	}
}

func TestApplyWithNothingPendingDoesNotPublish(t *testing.T) {
	s := &fakeStore{}
	res, err := Apply(context.Background(), s, domain.Perm, []string{"gone"}, time.Now)
	if err != nil || len(s.published) != 0 || !reflect.DeepEqual(res, Result{}) {
		t.Fatalf("result %+v, published %d, err %v", res, len(s.published), err)
	}
}

func TestApplyLeavesDeferredRowsUntouched(t *testing.T) {
	unknown := raw("r1", "event:5", `{}`)
	unknown.Source = domain.SyntheticSource
	s := &fakeStore{pending: []Raw{unknown}}
	res, err := Apply(context.Background(), s, domain.Perm, []string{"r1"}, time.Now)
	if err != nil || len(s.published) != 0 || res.Deferred != 1 {
		t.Fatalf("result %+v, published %d, err %v", res, len(s.published), err)
	}
}

func TestApplyReportsStoreErrors(t *testing.T) {
	s := &fakeStore{err: errors.New("down")}
	if _, err := Apply(context.Background(), s, domain.Perm, []string{"r1"}, time.Now); err == nil {
		t.Fatal("no error")
	}
}

func TestApplyReportsWhyRecordsFailed(t *testing.T) {
	broken := raw("r1", "node/1", `{"type":"node","id":1,"lat":58,"lon":56,"tags":{"amenity":"cafe"}}`)
	res, err := Apply(context.Background(), &fakeStore{pending: []Raw{broken}}, domain.Perm, []string{"r1"}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failures) != 1 ||
		res.Failures[0] != (Failure{RawIngestID: "r1", Source: domain.OSM, Code: "missing_name"}) {
		t.Fatalf("failures %+v", res.Failures)
	}
}

type timedStore struct {
	fakeStore
	at time.Time
}

func (s *timedStore) Publish(
	_ context.Context,
	_ domain.City,
	o *Outcome,
	at time.Time,
) (revision int64, published bool, err error) {
	s.at = at
	return 1, len(o.Apply) > 0, nil
}

func TestApplyPublishesAtTheNormalizationClock(t *testing.T) {
	calls := 0
	clock := func() time.Time {
		calls++
		return time.Date(2026, 9, 28, 10, 0, calls, 0, time.FixedZone("MSK", 3*3600))
	}
	s := &timedStore{pending: []Raw{raw("r1", "node/1", cafe)}}
	if _, err := Apply(context.Background(), s, domain.Perm, []string{"r1"}, clock); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || s.at.Location() != time.UTC {
		t.Fatalf("clock read %d times, published at %v", calls, s.at)
	}
}

func TestPrepareNormalizesMkrfEvents(t *testing.T) {
	r := raw("r1", "event:1", `{"data":{"general":{"name":"Лекция","places":[]}}}`)
	r.Source = domain.MkrfEvents
	o, deferred := Prepare(domain.Moscow, []Raw{r}, time.Now())
	if len(deferred) != 0 || len(o.Failed) != 1 || o.Failed[0].Code != "missing_place" {
		t.Fatalf("outcome %+v deferred %v", o, deferred)
	}
}

func TestPrepareNormalizesKudaGoEvents(t *testing.T) {
	r := raw("r1", "event:1", `{"title":"Концерт","place":null}`)
	r.Source = domain.KudaGo
	o, deferred := Prepare(domain.Moscow, []Raw{r}, time.Now())
	if len(deferred) != 0 || len(o.Failed) != 1 || o.Failed[0].Code != "missing_place" {
		t.Fatalf("outcome %+v deferred %v", o, deferred)
	}
}

const cafeWithoutCoordinates = `{"type":"node","id":7,"tags":{"name":"Кафе","amenity":"cafe"}}`

func TestPrepareQuarantinesMalformedRecords(t *testing.T) {
	unreadable := raw("r1", "node/1", `not json`)
	nowhere := raw("r2", "node/2", cafeWithoutCoordinates)
	nameless := raw("r3", "node/3", `{"type":"node","id":3,"lat":58,"lon":56,"tags":{"amenity":"cafe"}}`)

	o, _ := Prepare(domain.Perm, []Raw{unreadable, nowhere, nameless}, time.Now())

	want := []Quarantined{
		{Raw: unreadable, Reason: domain.InvalidSchema, Details: QuarantineDetails{Code: "bad_payload"}},
		{Raw: nowhere, Reason: domain.CorruptedGeometry, Details: QuarantineDetails{Code: "bad_coordinates"}},
	}
	if !reflect.DeepEqual(o.Quarantined, want) {
		t.Fatalf("quarantined %+v", o.Quarantined)
	}
	if len(o.Failed) != 1 || o.Failed[0].Code != "missing_name" {
		t.Fatalf("failed %+v", o.Failed)
	}
}

func TestApplyQuarantinesPlacesOutsideTheCity(t *testing.T) {
	far := raw(
		"r2",
		"node/2",
		`{"type":"node","id":2,"lat":58.01,"lon":57.5,"tags":{"name":"Далеко","amenity":"cafe"}}`,
	)
	s := &fakeStore{pending: []Raw{raw("r1", "node/1", cafe), far}, outside: []bool{false, true}, known: true}

	res, err := Apply(context.Background(), s, domain.Perm, []string{"r1", "r2"}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.points) != 2 || s.points[1] != (Point{Lat: 58.01, Lon: 57.5}) {
		t.Fatalf("points %+v", s.points)
	}
	if res.Applied != 1 || res.Quarantined != 1 || len(s.published) != 1 {
		t.Fatalf("result %+v", res)
	}
	got := s.published[0].Quarantined
	want := []Quarantined{{Raw: far, Reason: domain.GeoDiscrepancy,
		Details: QuarantineDetails{Check: "outside_city_boundary", Lat: 58.01, Lon: 57.5}}}
	if !reflect.DeepEqual(got, want) || s.published[0].Apply[0].Raw.ID != "r1" {
		t.Fatalf("published %+v", s.published[0])
	}
	if len(res.Failures) != 1 || res.Failures[0] != (Failure{RawIngestID: "r2", Source: domain.OSM,
		Code: "GEO_DISCREPANCY_QUARANTINE"}) {
		t.Fatalf("failures %+v", res.Failures)
	}
}

func TestApplyWithoutBoundaryAppliesEverything(t *testing.T) {
	before := testutil.ToFloat64(geoChecks.WithLabelValues("perm", "no_boundary"))
	s := &fakeStore{pending: []Raw{raw("r1", "node/1", cafe), raw("r2", "node/2", cafe)}}
	res, err := Apply(context.Background(), s, domain.Perm, []string{"r1", "r2"}, time.Now)
	if err != nil || res.Applied != 2 || res.Quarantined != 0 {
		t.Fatalf("result %+v, err %v", res, err)
	}
	if testutil.ToFloat64(geoChecks.WithLabelValues("perm", "no_boundary")) != before+2 {
		t.Fatal("unchecked places not counted")
	}
}

func TestApplyPublishesAQuarantineOnlyBatch(t *testing.T) {
	before := testutil.ToFloat64(ingest.SchemaMismatch.WithLabelValues("osm"))
	s := &fakeStore{pending: []Raw{raw("r1", "node/1", `not json`)}, known: true}
	res, err := Apply(context.Background(), s, domain.Perm, []string{"r1"}, time.Now)
	if err != nil || res.Quarantined != 1 || len(s.published) != 1 || s.points != nil {
		t.Fatalf("result %+v, published %d, points %v, err %v", res, len(s.published), s.points, err)
	}
	if testutil.ToFloat64(ingest.SchemaMismatch.WithLabelValues("osm")) != before+1 {
		t.Fatal("malformed payload not counted as a schema mismatch")
	}
}

func TestApplyRefusesAnIncompleteBoundaryAnswer(t *testing.T) {
	s := &fakeStore{pending: []Raw{raw("r1", "node/1", cafe)}, known: true}
	if _, err := Apply(context.Background(), s, domain.Perm, []string{"r1"}, time.Now); err == nil ||
		len(s.published) != 0 {
		t.Fatalf("err %v, published %d", err, len(s.published))
	}
}
