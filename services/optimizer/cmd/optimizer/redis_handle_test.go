package main

import (
	"context"
	"testing"

	"github.com/andres1m/impuls-goroda/pkg/redis"
	goredis "github.com/redis/go-redis/v9"
)

func TestRedisHandleLivesBetweenStartAndStop(t *testing.T) {
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:0"})
	defer client.Close()
	h := &redisHandle{redis: &redis.RedisClient{}}
	if h.get() != nil {
		t.Fatal("client before start")
	}
	h.redis.Pool = client
	if err := h.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.get() != client {
		t.Fatal("no client after start")
	}
	if err := h.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.get() != nil {
		t.Fatal("client after stop")
	}
}
