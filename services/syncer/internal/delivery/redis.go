package delivery

import (
	"context"
	"errors"
	"fmt"

	goredis "github.com/redis/go-redis/v9"

	"github.com/andres1m/impuls-goroda/pkg/catalogevent"
)

type Publisher interface {
	Publish(ctx context.Context, channel string, message any) *goredis.IntCmd
}

// RedisSender announces new catalog revisions to the services that cache the catalog.
type RedisSender struct {
	// The client exists only once the Redis component has started.
	client func() Publisher
}

func NewRedisSender(client func() Publisher) RedisSender {
	return RedisSender{client: client}
}

func (s RedisSender) Send(ctx context.Context, item *Item) error {
	if _, err := catalogevent.Decode(item.Payload); err != nil {
		return fmt.Errorf("decode catalog event: %w", err)
	}
	client := s.client()
	if client == nil {
		return errors.New("redis is not connected")
	}
	if err := client.Publish(ctx, catalogevent.Channel, item.Payload).Err(); err != nil {
		return fmt.Errorf("publish catalog event to redis: %w", err)
	}
	return nil
}
