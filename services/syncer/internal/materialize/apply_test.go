package materialize

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
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
