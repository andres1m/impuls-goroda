package catalogcache

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

var _ net.Error = timeoutError{}

type reply struct {
	msg any
	err error
}

// scriptedPubSub plays replies in order, then reports the script's end by cancelling the run.
type scriptedPubSub struct {
	mu      sync.Mutex
	replies []reply
	pingErr error
	pings   int
	closed  bool
	done    func()
}

func (p *scriptedPubSub) Receive(context.Context, time.Duration) (any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.replies) == 0 {
		p.done()
		return nil, context.Canceled
	}
	r := p.replies[0]
	p.replies = p.replies[1:]
	return r.msg, r.err
}

func (p *scriptedPubSub) Ping(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pings++
	return p.pingErr
}

func (p *scriptedPubSub) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	return nil
}

func subscribed() reply {
	return reply{msg: &goredis.Subscription{Kind: "subscribe", Channel: "catalog:revision", Count: 1}}
}

func message(payload string) reply {
	return reply{msg: &goredis.Message{Channel: "catalog:revision", Payload: payload}}
}

// run plays the scripts, one per connection, and reports whether the cache was healthy after each reply.
func run(t *testing.T, c *Cache, scripts ...*scriptedPubSub) []bool {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var health []bool
	opened := 0
	open := func(context.Context) PubSub {
		p := scripts[min(opened, len(scripts)-1)]
		opened++
		if opened >= len(scripts) {
			p.done = cancel
		} else {
			p.done = func() {}
		}
		return p
	}
	s := NewSubscriber(c, open, zap.NewNop())
	s.afterReply = func() { health = append(health, c.healthy.Load()) }
	s.pause = func(context.Context, time.Duration) {}
	if err := s.Run(ctx); err != nil {
		t.Fatal(err)
	}
	return health
}

func TestSubscriptionTurnsHealthyAfterReconciling(t *testing.T) {
	loader := &fakeLoader{revision: 3}
	c := newCache(t, loader, &fakeStore{})
	candidates(t, c)
	health := run(t, c, &scriptedPubSub{replies: []reply{subscribed()}})
	if len(health) != 1 || !health[0] {
		t.Fatalf("health %v", health)
	}
	if revisions, _ := loader.calls(); revisions != 2 {
		t.Fatalf("%d revision reads: the subscription did not reconcile", revisions)
	}
}

func TestSubscriptionStaysUnhealthyWhenReconcilingFails(t *testing.T) {
	loader := &fakeLoader{revision: 3}
	c := newCache(t, loader, &fakeStore{})
	candidates(t, c)
	loader.revErr = errors.New("db down")
	health := run(t, c, &scriptedPubSub{replies: []reply{subscribed()}})
	if len(health) != 1 || health[0] {
		t.Fatalf("health %v", health)
	}
}

func TestLostConnectionTurnsUnhealthyUntilResubscribed(t *testing.T) {
	c := newCache(t, &fakeLoader{revision: 3}, &fakeStore{})
	health := run(t, c, &scriptedPubSub{replies: []reply{subscribed(), {err: errors.New("connection reset")}, subscribed()}})
	if len(health) != 3 || !health[0] || health[1] || !health[2] {
		t.Fatalf("health %v", health)
	}
}

func TestSilentConnectionIsPingedAndDroppedWhenPingFails(t *testing.T) {
	c := newCache(t, &fakeLoader{revision: 3}, &fakeStore{})
	first := &scriptedPubSub{replies: []reply{subscribed(), {err: timeoutError{}}}, pingErr: errors.New("broken pipe")}
	second := &scriptedPubSub{replies: []reply{subscribed()}}
	health := run(t, c, first, second)
	if len(health) != 3 || !health[0] || health[1] || !health[2] {
		t.Fatalf("health %v", health)
	}
	if first.pings != 1 || !first.closed {
		t.Fatalf("pings %d, closed %v", first.pings, first.closed)
	}
}

func TestAnnouncementReachesTheCache(t *testing.T) {
	loader := &fakeLoader{revision: 3}
	c := newCache(t, loader, &fakeStore{})
	candidates(t, c)
	loader.mu.Lock()
	loader.revision = 4
	loader.mu.Unlock()
	run(t, c, &scriptedPubSub{replies: []reply{
		subscribed(),
		message(`{"city":"perm","catalog_revision":4,"reason":"urgent","published_at":"2026-09-28T05:59:59Z"}`),
		message(`{"city":""}`),
	}})
	c.waitWarm()
	if c.healthy.Load() {
		t.Fatal("the cache trusts memory after the subscriber stopped")
	}
	c.SetHealthy(true)
	revisions, _ := loader.calls()
	if data := candidates(t, c); data.CatalogRevision != 4 {
		t.Fatalf("revision %d", data.CatalogRevision)
	}
	if after, _ := loader.calls(); after != revisions {
		t.Fatal("the announced slice is not trusted")
	}
}

func TestHealthySubscriptionReconcilesPeriodically(t *testing.T) {
	loader := &fakeLoader{revision: 3}
	clock := now
	c, err := New(Config{}, loader, &fakeStore{}, zap.NewNop(), WithClock(func() time.Time { return clock }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	candidates(t, c)
	ps := &scriptedPubSub{replies: []reply{subscribed(), {msg: &goredis.Pong{}}, {msg: &goredis.Pong{}}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ps.done = cancel
	s := NewSubscriber(c, func(context.Context) PubSub { return ps }, zap.NewNop())
	s.afterReply = func() { clock = clock.Add(40 * time.Second) }
	if err := s.Run(ctx); err != nil {
		t.Fatal(err)
	}
	// One read to build the slice, one on subscribing, one when the minute since then has passed.
	if revisions, _ := loader.calls(); revisions != 3 {
		t.Fatalf("%d revision reads", revisions)
	}
}
