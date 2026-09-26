package auth

import (
	"errors"
	"math"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type RateLimitConfig struct {
	RequestsPerMinute int
	Burst             int
	IdleTTL           time.Duration
}

type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type RateLimiter struct {
	mu      sync.Mutex
	rate    rate.Limit
	burst   int
	idleTTL time.Duration
	entries map[string]*limiterEntry
}

func NewRateLimiter(cfg RateLimitConfig) (*RateLimiter, error) {
	if cfg.RequestsPerMinute <= 0 || cfg.Burst <= 0 || cfg.IdleTTL <= 0 {
		return nil, errors.New("invalid rate limit configuration")
	}
	return &RateLimiter{
		rate:    rate.Limit(float64(cfg.RequestsPerMinute) / 60),
		burst:   cfg.Burst,
		idleTTL: cfg.IdleTTL,
		entries: make(map[string]*limiterEntry),
	}, nil
}

func (l *RateLimiter) Allow(key string, now time.Time) (bool, time.Duration) {
	if key == "" {
		return false, time.Second
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.entries[key]
	if !ok {
		entry = &limiterEntry{limiter: rate.NewLimiter(l.rate, l.burst)}
		l.entries[key] = entry
	}
	entry.lastSeen = now
	if entry.limiter.AllowN(now, 1) {
		return true, 0
	}
	reservation := entry.limiter.ReserveN(now, 1)
	if !reservation.OK() {
		return false, time.Second
	}
	delay := reservation.DelayFrom(now)
	reservation.CancelAt(now)
	if delay < time.Second {
		delay = time.Second
	}
	return false, time.Duration(math.Ceil(delay.Seconds())) * time.Second
}

func (l *RateLimiter) Cleanup(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for key, entry := range l.entries {
		if !entry.lastSeen.Add(l.idleTTL).After(now) {
			delete(l.entries, key)
		}
	}
}
