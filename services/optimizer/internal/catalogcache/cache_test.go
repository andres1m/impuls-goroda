package catalogcache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/andres1m/impuls-goroda/pkg/catalogevent"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/catalogslice"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/usecase"
)

var (
	now    = time.Date(2026, 9, 28, 6, 0, 0, 0, time.UTC)
	center = domain.Coordinate{Longitude: 56.2294, Latitude: 58.0105}
)

func request() domain.OptimizeRequest {
	return domain.OptimizeRequest{
		City: "perm", Timezone: "Asia/Yekaterinburg", Start: now.Add(time.Hour), End: now.Add(9 * time.Hour), Origin: center,
		Constraints: domain.RouteConstraints{MovementModes: []domain.MovementMode{domain.MovementWalk}, LoadProfile: "moderate", Budget: domain.Budget{Mode: domain.BudgetNone}},
	}
}

func sliceAt(revision domain.CatalogRevision, horizon time.Time) *catalogslice.Slice {
	return &catalogslice.Slice{City: "perm", Timezone: "Asia/Yekaterinburg", Revision: revision, UpdatedAt: now, BuiltAt: now, Horizon: horizon}
}

type fakeLoader struct {
	mu        sync.Mutex
	revision  domain.CatalogRevision
	revErr    error
	loadErr   error
	revCalls  int
	loads     []time.Time
	byID      [][]domain.SessionID
	loadDelay time.Duration
}

func (l *fakeLoader) Revision(context.Context, string) (domain.CatalogRevision, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.revCalls++
	return l.revision, l.revErr
}

func (l *fakeLoader) LoadSlice(_ context.Context, _ string, horizon time.Time) (*catalogslice.Slice, error) {
	time.Sleep(l.loadDelay)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.loads = append(l.loads, horizon)
	if l.loadErr != nil {
		return nil, l.loadErr
	}
	return sliceAt(l.revision, horizon), nil
}

func (l *fakeLoader) SessionsByID(_ context.Context, _ *catalogslice.Slice, ids []domain.SessionID) ([]domain.Candidate, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.byID = append(l.byID, ids)
	return nil, nil
}

func (l *fakeLoader) calls() (revisions, loads int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.revCalls, len(l.loads)
}

type fakeStore struct {
	mu     sync.Mutex
	slices map[domain.CatalogRevision]*catalogslice.Slice
	getErr error
	puts   atomic.Int32
}

func (s *fakeStore) Get(_ context.Context, _ string, revision domain.CatalogRevision) (*catalogslice.Slice, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getErr != nil {
		return nil, false, s.getErr
	}
	sl, ok := s.slices[revision]
	return sl, ok, nil
}

func (s *fakeStore) Put(_ context.Context, sl *catalogslice.Slice) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.slices == nil {
		s.slices = map[domain.CatalogRevision]*catalogslice.Slice{}
	}
	s.slices[sl.Revision] = sl
	s.puts.Add(1)
	return nil
}

func newCache(t *testing.T, loader Loader, store SliceStore) *Cache {
	t.Helper()
	c, err := New(Config{}, loader, store, zap.NewNop(), WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

func candidates(t *testing.T, c *Cache) domain.DataFreshness {
	t.Helper()
	_, data, err := c.Candidates(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestTrustedSliceNeedsNoDatabase(t *testing.T) {
	loader := &fakeLoader{revision: 3}
	c := newCache(t, loader, &fakeStore{})
	c.SetHealthy(true)
	candidates(t, c)
	candidates(t, c)
	candidates(t, c)
	if revisions, loads := loader.calls(); revisions != 1 || loads != 1 {
		t.Fatalf("%d revision reads and %d loads for three requests", revisions, loads)
	}
	if horizon := loader.loads[0]; !horizon.Equal(now.Add(-24 * time.Hour)) {
		t.Fatalf("slice horizon %v", horizon)
	}
}

func TestUnhealthySubscriptionChecksEveryRequest(t *testing.T) {
	loader := &fakeLoader{revision: 3}
	c := newCache(t, loader, &fakeStore{})
	candidates(t, c)
	candidates(t, c)
	if revisions, loads := loader.calls(); revisions != 2 || loads != 1 {
		t.Fatalf("%d revision reads and %d loads", revisions, loads)
	}
}

func TestNewerRevisionReplacesTheSlice(t *testing.T) {
	loader := &fakeLoader{revision: 3}
	c := newCache(t, loader, &fakeStore{})
	c.SetHealthy(true)
	candidates(t, c)
	loader.mu.Lock()
	loader.revision = 4
	loader.mu.Unlock()
	c.Observe("perm", 4)
	if data := candidates(t, c); data.CatalogRevision != 4 {
		t.Fatalf("served revision %d after hearing of 4", data.CatalogRevision)
	}
}

func TestAnnouncementWarmsACachedCity(t *testing.T) {
	loader := &fakeLoader{revision: 3}
	c := newCache(t, loader, &fakeStore{})
	c.SetHealthy(true)
	candidates(t, c)
	loader.mu.Lock()
	loader.revision = 5
	loader.mu.Unlock()
	c.Announce(catalogevent.Invalidation{City: "perm", CatalogRevision: 5, Reason: catalogevent.ReasonUrgent, PublishedAt: now})
	c.waitWarm()
	if _, loads := loader.calls(); loads != 2 {
		t.Fatalf("%d loads after the announcement", loads)
	}
	revisions, _ := loader.calls()
	if data := candidates(t, c); data.CatalogRevision != 5 {
		t.Fatalf("served revision %d", data.CatalogRevision)
	}
	if after, _ := loader.calls(); after != revisions {
		t.Fatal("the warmed slice is not trusted")
	}
}

func TestOldAnnouncementIsIgnored(t *testing.T) {
	loader := &fakeLoader{revision: 3}
	c := newCache(t, loader, &fakeStore{})
	c.SetHealthy(true)
	candidates(t, c)
	c.Announce(catalogevent.Invalidation{City: "perm", CatalogRevision: 2, Reason: catalogevent.ReasonSeed, PublishedAt: now})
	c.Announce(catalogevent.Invalidation{City: "moscow", CatalogRevision: 9, Reason: catalogevent.ReasonSeed, PublishedAt: now})
	c.waitWarm()
	candidates(t, c)
	if revisions, loads := loader.calls(); revisions != 1 || loads != 1 {
		t.Fatalf("%d revision reads and %d loads", revisions, loads)
	}
}

func TestSliceComesFromTheSharedStore(t *testing.T) {
	loader := &fakeLoader{revision: 3}
	store := &fakeStore{slices: map[domain.CatalogRevision]*catalogslice.Slice{3: sliceAt(3, now.Add(-24*time.Hour))}}
	c := newCache(t, loader, store)
	candidates(t, c)
	if _, loads := loader.calls(); loads != 0 || store.puts.Load() != 0 {
		t.Fatalf("%d loads, %d puts", loads, store.puts.Load())
	}
}

func TestBrokenSharedStoreFallsBackToTheDatabase(t *testing.T) {
	loader := &fakeLoader{revision: 3}
	c := newCache(t, loader, &fakeStore{getErr: errors.New("redis down")})
	if data := candidates(t, c); data.CatalogRevision != 3 {
		t.Fatalf("revision %d", data.CatalogRevision)
	}
}

func TestConcurrentMissesLoadOnce(t *testing.T) {
	loader := &fakeLoader{revision: 3, loadDelay: 20 * time.Millisecond}
	c := newCache(t, loader, &fakeStore{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { candidates(t, c) })
	}
	wg.Wait()
	if _, loads := loader.calls(); loads != 1 {
		t.Fatalf("%d loads for concurrent misses", loads)
	}
}

func TestDatabaseDownWithoutTrustRefuses(t *testing.T) {
	loader := &fakeLoader{revision: 3}
	c := newCache(t, loader, &fakeStore{})
	candidates(t, c)
	loader.mu.Lock()
	loader.revErr = usecase.ErrUnavailable
	loader.mu.Unlock()
	if _, _, err := c.Candidates(context.Background(), request()); !errors.Is(err, usecase.ErrUnavailable) {
		t.Fatalf("got %v", err)
	}
}

func TestReconcile(t *testing.T) {
	loader := &fakeLoader{revision: 3}
	c := newCache(t, loader, &fakeStore{})
	candidates(t, c)

	loader.mu.Lock()
	loader.revErr = usecase.ErrUnavailable
	loader.mu.Unlock()
	if err := c.Reconcile(context.Background()); err == nil {
		t.Fatal("reconciled without the database")
	}

	loader.mu.Lock()
	loader.revErr, loader.revision = nil, 4
	loader.mu.Unlock()
	if err := c.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.waitWarm()
	c.SetHealthy(true)
	revisions, _ := loader.calls()
	if data := candidates(t, c); data.CatalogRevision != 4 {
		t.Fatalf("revision %d after reconciling", data.CatalogRevision)
	}
	if after, _ := loader.calls(); after != revisions {
		t.Fatal("reconciled slice is not trusted")
	}
}

func TestRequestBeforeTheHorizonIsNotCached(t *testing.T) {
	loader := &fakeLoader{revision: 3}
	store := &fakeStore{}
	c := newCache(t, loader, store)
	req := request()
	req.Start, req.End = now.Add(-48*time.Hour), now.Add(-40*time.Hour)
	if _, _, err := c.Candidates(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if loader.loads[len(loader.loads)-1] != req.Start || store.puts.Load() != 1 {
		t.Fatalf("loads %v, puts %d", loader.loads, store.puts.Load())
	}
}

func TestObligationOutsideTheSliceIsRead(t *testing.T) {
	loader := &fakeLoader{revision: 3}
	c := newCache(t, loader, &fakeStore{})
	req := request()
	old := domain.SessionID{7}
	req.Constraints.Obligations = []domain.Obligation{{SessionID: &old, Participation: domain.ParticipationUserReported}}
	if _, _, err := c.Candidates(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if len(loader.byID) != 1 || loader.byID[0][0] != old {
		t.Fatalf("read by id %v", loader.byID)
	}
}

func TestInvalidRequest(t *testing.T) {
	c := newCache(t, &fakeLoader{revision: 3}, &fakeStore{})
	req := request()
	req.City = ""
	if _, _, err := c.Candidates(context.Background(), req); !errors.Is(err, usecase.ErrInvalidRequest) {
		t.Fatalf("got %v", err)
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	if _, err := New(Config{L1MaxBytes: -1}, &fakeLoader{}, &fakeStore{}, zap.NewNop()); err == nil {
		t.Fatal("accepted")
	}
}
