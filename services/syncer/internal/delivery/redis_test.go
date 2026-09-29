package delivery

import (
	"context"
	"testing"

	goredis "github.com/redis/go-redis/v9"
)

type fakePublisher struct {
	channel string
	message any
}

func (p *fakePublisher) Publish(ctx context.Context, channel string, message any) *goredis.IntCmd {
	p.channel, p.message = channel, message
	return goredis.NewIntCmd(ctx)
}

const payload = `{"city":"perm","catalog_revision":2,"reason":"seed","published_at":"2026-09-28T05:00:00Z"}`

func TestRedisSenderPublishesTheRevision(t *testing.T) {
	p := &fakePublisher{}
	sender := NewRedisSender(func() Publisher { return p })
	if err := sender.Send(context.Background(), &Item{Payload: []byte(payload)}); err != nil {
		t.Fatal(err)
	}
	if p.channel != "catalog:revision" || string(p.message.([]byte)) != payload {
		t.Fatalf("published %q to %q", p.message, p.channel)
	}
}

func TestRedisSenderRefusesABrokenPayload(t *testing.T) {
	p := &fakePublisher{}
	sender := NewRedisSender(func() Publisher { return p })
	if err := sender.Send(context.Background(), &Item{Payload: []byte(`{"city":""}`)}); err == nil || p.channel != "" {
		t.Fatalf("broken payload published: err %v, channel %q", err, p.channel)
	}
}

func TestRedisSenderWithoutClient(t *testing.T) {
	sender := NewRedisSender(func() Publisher { return nil })
	if err := sender.Send(context.Background(), &Item{Payload: []byte(payload)}); err == nil {
		t.Fatal("sent without a client")
	}
}
