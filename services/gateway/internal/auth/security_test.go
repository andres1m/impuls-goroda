package auth

import (
	"testing"
	"time"
)

func TestRateLimiterBurstRefillAndCleanup(t *testing.T) {
	limiter, err := NewRateLimiter(RateLimitConfig{RequestsPerMinute: 60, Burst: 2, IdleTTL: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		if allowed, _ := limiter.Allow("user", now); !allowed {
			t.Fatalf("burst request %d rejected", i)
		}
	}
	if allowed, retry := limiter.Allow("user", now); allowed || retry != time.Second {
		t.Fatalf("allowed=%v retry=%v", allowed, retry)
	}
	if allowed, _ := limiter.Allow("user", now.Add(time.Second)); !allowed {
		t.Fatal("refilled token rejected")
	}
	limiter.Cleanup(now.Add(11 * time.Minute))
	if len(limiter.entries) != 0 {
		t.Fatal("idle limiter was not removed")
	}
}

func TestWebhookVerifier(t *testing.T) {
	verifier, err := NewWebhookVerifier("secret")
	if err != nil {
		t.Fatal(err)
	}
	if !verifier.Valid("secret") || verifier.Valid("wrong") || verifier.Valid("secret-long") {
		t.Fatal("webhook secret verification failed")
	}
}
