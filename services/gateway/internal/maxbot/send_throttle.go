package maxbot

import (
	"context"
	"errors"
	"sync"
	"time"
)

const recipientSendInterval = 500 * time.Millisecond
const maxSendRecipients = 4096

var errSendThrottleFull = errors.New("MAX send capacity unavailable")

type recipientSend struct {
	busy bool
	next time.Time
	done chan struct{}
}

type sendThrottle struct {
	mu         sync.Mutex
	recipients map[int64]*recipientSend
}

func (g *sendThrottle) acquire(ctx context.Context, recipient int64) (func(), error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		g.mu.Lock()
		now := time.Now()
		for id, entry := range g.recipients {
			if !entry.busy && !now.Before(entry.next) {
				delete(g.recipients, id)
			}
		}
		entry := g.recipients[recipient]
		if entry == nil {
			if len(g.recipients) >= maxSendRecipients {
				g.mu.Unlock()
				return nil, errSendThrottleFull
			}
			if g.recipients == nil {
				g.recipients = make(map[int64]*recipientSend)
			}
			entry = &recipientSend{}
			g.recipients[recipient] = entry
		}
		if entry.busy {
			done := entry.done
			g.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
			}
			continue
		}
		if delay := entry.next.Sub(now); delay > 0 {
			g.mu.Unlock()
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
			continue
		}
		entry.busy = true
		entry.done = make(chan struct{})
		g.mu.Unlock()
		var once sync.Once
		return func() {
			once.Do(func() {
				g.mu.Lock()
				entry.next = time.Now().Add(recipientSendInterval)
				entry.busy = false
				close(entry.done)
				g.mu.Unlock()
			})
		}, nil
	}
}
