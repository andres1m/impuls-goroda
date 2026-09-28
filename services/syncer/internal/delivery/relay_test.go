package delivery

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

var now = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

type mark struct {
	id   uuid.UUID
	next time.Time
}

type fakeStore struct {
	mu           sync.Mutex
	queue        []Item
	claimErr     error
	destinations [][]string
	leases       []time.Time
	delivered    []uuid.UUID
	failed       []mark
	claims       int
	// Leases the marks were made under.
	markLeases []time.Time
}

func (s *fakeStore) Claim(_ context.Context, _ time.Time, leaseUntil time.Time, destinations []string, limit int) ([]Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claims++
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	s.destinations = append(s.destinations, destinations)
	s.leases = append(s.leases, leaseUntil)
	n := min(limit, len(s.queue))
	out := slices.Clone(s.queue[:n])
	s.queue = s.queue[n:]
	for i := range out {
		out[i].LeaseUntil = leaseUntil
	}
	return out, nil
}

func (s *fakeStore) Delivered(_ context.Context, item Item, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delivered = append(s.delivered, item.ID)
	s.markLeases = append(s.markLeases, item.LeaseUntil)
	return nil
}

func (s *fakeStore) Failed(_ context.Context, item Item, next time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed = append(s.failed, mark{item.ID, next})
	s.markLeases = append(s.markLeases, item.LeaseUntil)
	return nil
}

func (s *fakeStore) claimCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.claims
}

func (s *fakeStore) Backlog(context.Context, time.Time) (int, time.Duration, error) { return 0, 0, nil }

type fakeSender struct {
	err  error
	sent []uuid.UUID
}

func (f *fakeSender) Send(_ context.Context, item Item) error {
	f.sent = append(f.sent, item.ID)
	return f.err
}

func items(n int, attempts int) []Item {
	out := make([]Item, n)
	for i := range out {
		out[i] = Item{ID: uuid.New(), Destination: "redis", EventType: "catalog.revision", Payload: []byte(`{}`), Attempts: attempts, CreatedAt: now}
	}
	return out
}

func testConfig() Config {
	return Config{PollInterval: time.Second, Batch: 2, Lease: 30 * time.Second, BackoffMin: time.Second, BackoffMax: time.Minute}
}

func newTestRelay(t *testing.T, store Store, senders map[string]Sender) *Relay {
	t.Helper()
	r, err := NewRelay(testConfig(), store, senders, zap.NewNop(), WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestTickDeliversAndMarks(t *testing.T) {
	store := &fakeStore{queue: items(1, 1)}
	sender := &fakeSender{}
	if err := newTestRelay(t, store, map[string]Sender{"redis": sender}).Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 1 || len(store.delivered) != 1 || store.delivered[0] != sender.sent[0] || len(store.failed) != 0 {
		t.Fatalf("sent %v delivered %v failed %v", sender.sent, store.delivered, store.failed)
	}
	if !store.leases[0].Equal(now.Add(30*time.Second)) || !store.markLeases[0].Equal(store.leases[0]) {
		t.Fatalf("lease until %v, marked under %v", store.leases[0], store.markLeases)
	}
}

func TestTickBacksOffAFailedSend(t *testing.T) {
	store := &fakeStore{queue: items(1, 3)}
	sender := &fakeSender{err: errors.New("redis down")}
	if err := newTestRelay(t, store, map[string]Sender{"redis": sender}).Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.failed) != 1 || !store.failed[0].next.Equal(now.Add(4*time.Second)) || len(store.delivered) != 0 {
		t.Fatalf("failed %v delivered %v", store.failed, store.delivered)
	}
}

func TestTickDrainsFullBatches(t *testing.T) {
	store := &fakeStore{queue: items(5, 1)}
	sender := &fakeSender{}
	if err := newTestRelay(t, store, map[string]Sender{"redis": sender}).Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.delivered) != 5 || len(store.destinations) != 3 {
		t.Fatalf("delivered %d in %d claims", len(store.delivered), len(store.destinations))
	}
}

func TestTickClaimsOnlyRegisteredDestinations(t *testing.T) {
	store := &fakeStore{}
	senders := map[string]Sender{"redis": &fakeSender{}, "gateway": &fakeSender{}}
	if err := newTestRelay(t, store, senders).Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := store.destinations[0]; !slices.Equal(got, []string{"gateway", "redis"}) {
		t.Fatalf("claimed destinations %v", got)
	}
}

func TestTickReturnsClaimError(t *testing.T) {
	store := &fakeStore{claimErr: errors.New("db down")}
	if err := newTestRelay(t, store, map[string]Sender{"redis": &fakeSender{}}).Tick(context.Background()); err == nil {
		t.Fatal("claim error swallowed")
	}
}

func TestRunKeepsPollingAfterErrors(t *testing.T) {
	store := &fakeStore{claimErr: errors.New("db down")}
	cfg := testConfig()
	cfg.PollInterval = time.Millisecond
	r, err := NewRelay(cfg, store, map[string]Sender{"redis": &fakeSender{}}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := r.Run(ctx); err != nil {
		t.Fatalf("run stopped with %v", err)
	}
	if n := store.claimCount(); n < 2 {
		t.Fatalf("%d claims: the relay stopped polling after an error", n)
	}
}

func TestRunWarnsOncePerOutage(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	store := &fakeStore{claimErr: errors.New("db down")}
	cfg := testConfig()
	cfg.PollInterval = time.Millisecond
	r, err := NewRelay(cfg, store, map[string]Sender{"redis": &fakeSender{}}, zap.New(core))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	for store.claimCount() < 5 {
		time.Sleep(time.Millisecond)
	}
	store.mu.Lock()
	store.claimErr = nil
	store.mu.Unlock()
	for n := store.claimCount(); store.claimCount() < n+2; {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var levels []zapcore.Level
	for _, e := range logs.All() {
		levels = append(levels, e.Level)
	}
	if !slices.Equal(levels, []zapcore.Level{zapcore.WarnLevel, zapcore.InfoLevel}) {
		t.Fatalf("log levels %v", levels)
	}
}

// blockingSender holds a delivery until released.
type blockingSender struct {
	started chan struct{}
	release chan struct{}
}

func (b *blockingSender) Send(context.Context, Item) error {
	close(b.started)
	<-b.release
	return nil
}

func TestStopWaitsForTheDeliveryInProgress(t *testing.T) {
	store := &fakeStore{queue: items(1, 1)}
	sender := &blockingSender{started: make(chan struct{}), release: make(chan struct{})}
	r := newTestRelay(t, store, map[string]Sender{"redis": sender})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = r.Run(ctx) }()
	<-sender.started
	cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- r.Stop(context.Background()) }()
	select {
	case <-stopped:
		t.Fatal("stopped while a delivery was in progress")
	case <-time.After(20 * time.Millisecond):
	}
	close(sender.release)
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.delivered) != 1 {
		t.Fatal("the delivery was not marked before stopping")
	}
}

func TestStopWithoutRun(t *testing.T) {
	r := newTestRelay(t, &fakeStore{}, map[string]Sender{"redis": &fakeSender{}})
	if err := r.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestNewRelayRejectsInvalidSetup(t *testing.T) {
	good := testConfig()
	for name, tc := range map[string]struct {
		cfg     Config
		senders map[string]Sender
	}{
		"no senders":       {good, nil},
		"negative batch":   {Config{PollInterval: time.Second, Batch: -1, Lease: time.Second, BackoffMin: time.Second, BackoffMax: time.Second}, map[string]Sender{"redis": &fakeSender{}}},
		"backoff inverted": {Config{PollInterval: time.Second, Batch: 1, Lease: time.Second, BackoffMin: time.Minute, BackoffMax: time.Second}, map[string]Sender{"redis": &fakeSender{}}},
		"nil sender":       {good, map[string]Sender{"redis": nil}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewRelay(tc.cfg, &fakeStore{}, tc.senders, zap.NewNop()); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestConfigDefaults(t *testing.T) {
	r, err := NewRelay(Config{}, &fakeStore{}, map[string]Sender{"redis": &fakeSender{}}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if r.cfg != (Config{PollInterval: 500 * time.Millisecond, Batch: 100, Lease: 30 * time.Second, BackoffMin: time.Second, BackoffMax: time.Minute}) {
		t.Fatalf("defaults %+v", r.cfg)
	}
}

func TestBackoff(t *testing.T) {
	for attempts, want := range map[int]time.Duration{0: time.Second, 1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 7: time.Minute, 200: time.Minute} {
		if got := Backoff(attempts, time.Second, time.Minute); got != want {
			t.Errorf("attempt %d: %v, want %v", attempts, got, want)
		}
	}
}
