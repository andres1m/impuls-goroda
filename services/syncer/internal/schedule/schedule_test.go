package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
)

type fakeAdapter struct{ source domain.SourceKey }

func (a fakeAdapter) Source() domain.Source { return domain.Source{Key: a.source} }
func (fakeAdapter) Fetch(context.Context, domain.City, json.RawMessage) (ingest.Batch, error) {
	return ingest.Batch{}, nil
}

type fakeStore struct {
	last     map[domain.SourceKey]time.Time
	held     bool
	lockErr  error
	released int
}

func (s *fakeStore) LastAttempt(_ context.Context, source domain.SourceKey, _ domain.City) (time.Time, bool, error) {
	at, ok := s.last[source]
	return at, ok, nil
}

func (s *fakeStore) TryLock(context.Context, domain.SourceKey, domain.City) (func(), bool, error) {
	if s.lockErr != nil || s.held {
		return nil, false, s.lockErr
	}
	return func() { s.released++ }, true, nil
}

type fakeIngester struct {
	calls []domain.SourceKey
	err   error
}

func (i *fakeIngester) Ingest(_ context.Context, a ingest.Adapter, _ domain.City) (ingest.Result, error) {
	i.calls = append(i.calls, a.Source().Key)
	return ingest.Result{}, i.err
}

var noon = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func newScheduler(t *testing.T, store *fakeStore, ing *fakeIngester) *Scheduler {
	t.Helper()
	jobs := []Job{
		{Source: domain.KudaGo, City: domain.Moscow, Every: 6 * time.Hour},
		{Source: domain.OSM, City: domain.Moscow, Every: 2 * time.Hour},
	}
	adapters := map[domain.SourceKey]ingest.Adapter{
		domain.KudaGo: fakeAdapter{domain.KudaGo}, domain.OSM: fakeAdapter{domain.OSM},
	}
	s, err := New(jobs, adapters, ing, store, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return noon }
	return s
}

func TestRunDueCollectsOnlySourcesPastTheirInterval(t *testing.T) {
	store := &fakeStore{last: map[domain.SourceKey]time.Time{
		domain.KudaGo: noon.Add(-5 * time.Hour),
		domain.OSM:    noon.Add(-3 * time.Hour),
	}}
	ing := &fakeIngester{}
	newScheduler(t, store, ing).RunDue(context.Background())
	if len(ing.calls) != 1 || ing.calls[0] != domain.OSM {
		t.Fatalf("collected %v, want only osm", ing.calls)
	}
	if store.released != 2 {
		t.Fatalf("released %d locks, want 2", store.released)
	}
}

func TestRunDueCollectsSourcesNeverTried(t *testing.T) {
	ing := &fakeIngester{}
	newScheduler(t, &fakeStore{}, ing).RunDue(context.Background())
	if len(ing.calls) != 2 {
		t.Fatalf("collected %v", ing.calls)
	}
}

func TestRunDueSkipsSourceAnotherRunHolds(t *testing.T) {
	ing := &fakeIngester{}
	newScheduler(t, &fakeStore{held: true}, ing).RunDue(context.Background())
	if len(ing.calls) != 0 {
		t.Fatalf("collected %v while the lock was held", ing.calls)
	}
}

func TestRunDueSurvivesFailedCollection(t *testing.T) {
	ing := &fakeIngester{err: errors.New("source down")}
	store := &fakeStore{}
	newScheduler(t, store, ing).RunDue(context.Background())
	if len(ing.calls) != 2 || store.released != 2 {
		t.Fatalf("collected %v, released %d", ing.calls, store.released)
	}
}

func TestParseValidatesJobs(t *testing.T) {
	good := Config{Jobs: []JobConfig{{Source: "kudago", City: "moscow", Every: 6 * time.Hour}}}
	jobs, err := good.Parse()
	if err != nil || len(jobs) != 1 || jobs[0].Source != domain.KudaGo || jobs[0].City != domain.Moscow {
		t.Fatalf("got %+v, %v", jobs, err)
	}
	if jobs, err = (Config{}).Parse(); err != nil || len(jobs) != 0 {
		t.Fatalf("empty config: %+v, %v", jobs, err)
	}
	for name, bad := range map[string]JobConfig{
		"prepared export":  {Source: "mkrf_events", City: "moscow", Every: time.Hour},
		"synthetic":        {Source: "synthetic", City: "moscow", Every: time.Hour},
		"unknown source":   {Source: "bileter", City: "moscow", Every: time.Hour},
		"unknown city":     {Source: "osm", City: "spb", Every: time.Hour},
		"missing interval": {Source: "osm", City: "perm"},
		"too frequent":     {Source: "osm", City: "perm", Every: time.Second},
	} {
		if _, err := (Config{Jobs: []JobConfig{bad}}).Parse(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	dup := Config{Jobs: []JobConfig{good.Jobs[0], good.Jobs[0]}}
	if _, err := dup.Parse(); err == nil {
		t.Error("duplicate job accepted")
	}
}

func TestNewRejectsJobWithoutAdapter(t *testing.T) {
	_, err := New([]Job{{Source: domain.OSM, City: domain.Perm, Every: time.Hour}}, nil, &fakeIngester{}, &fakeStore{}, zap.NewNop())
	if err == nil {
		t.Fatal("accepted a job without an adapter")
	}
}
