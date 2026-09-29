package catalogcache

import (
	"context"
	"os"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/andres1m/impuls-goroda/pkg/catalogevent"
)

func testRedis(t *testing.T) *goredis.Client {
	t.Helper()
	addr := os.Getenv("OPTIMIZER_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("OPTIMIZER_TEST_REDIS_ADDR is not set")
	}
	client := goredis.NewClient(&goredis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	return client
}

func TestRedisStoreIntegration(t *testing.T) {
	client := testRedis(t)
	ctx := context.Background()
	store := NewRedisStore(func() goredis.UniversalClient { return client }, time.Minute)
	want := fullSlice(t)
	want.City = "test-" + time.Now().Format("150405.000000")
	t.Cleanup(func() { client.Del(context.Background(), key(want.City, want.Revision)) })

	if _, ok, err := store.Get(ctx, want.City, want.Revision); err != nil || ok {
		t.Fatalf("empty store: ok %v, %v", ok, err)
	}
	if err := store.Put(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.Get(ctx, want.City, want.Revision)
	if err != nil || !ok || got.Revision != want.Revision || len(got.Sessions) != 1 ||
		got.Places[0].Rules.Validate() != nil {
		t.Fatalf("stored slice: ok %v, %v, %+v", ok, err, got)
	}
	if ttl := client.TTL(ctx, key(want.City, want.Revision)).Val(); ttl <= 0 || ttl > time.Minute {
		t.Fatalf("ttl %v", ttl)
	}
	disconnected := NewRedisStore(func() goredis.UniversalClient { return nil }, time.Minute)
	if _, _, err := disconnected.Get(ctx, want.City, want.Revision); err == nil {
		t.Fatal("read without a client")
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSubscriberSurvivesAKilledConnectionIntegration(t *testing.T) {
	client := testRedis(t)
	loader := &fakeLoader{revision: 3}
	c, err := New(Config{HealthInterval: 200 * time.Millisecond}, loader, &fakeStore{}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	candidates(t, c)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = NewSubscriber(c, RedisPubSub(func() goredis.UniversalClient { return client }), zap.NewNop()).Run(ctx)
	}()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, "a healthy subscription", c.healthy.Load)

	loader.mu.Lock()
	loader.revision = 4
	loader.mu.Unlock()
	payload, err := catalogevent.Encode(
		catalogevent.Invalidation{
			City:            "perm",
			CatalogRevision: 4,
			Reason:          catalogevent.ReasonUrgent,
			PublishedAt:     time.Now(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Publish(context.Background(), catalogevent.Channel, payload).Err(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the announced revision", func() bool {
		s, ok := c.l1.Get("perm")
		return ok && s.Revision == 4
	})

	before, _ := loader.calls()
	if err := client.ClientKillByFilter(context.Background(), "TYPE", "pubsub").Err(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a reconciliation after the reconnect", func() bool {
		after, _ := loader.calls()
		return after > before && c.healthy.Load()
	})
}
