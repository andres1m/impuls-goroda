package auth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
)

type fakeIssuer struct {
	account domain.UserAccount
	session domain.AuthSession
	err     error
}

func (f *fakeIssuer) IssueMaxSession(_ context.Context, account domain.UserAccount, session domain.AuthSession) (domain.UserAccount, domain.AuthSession, error) {
	if f.err != nil {
		return domain.UserAccount{}, domain.AuthSession{}, f.err
	}
	f.account, f.session = account, session
	return account, session, nil
}

func (f *fakeIssuer) IssueTestSession(_ context.Context, account domain.UserAccount, session domain.AuthSession) (domain.UserAccount, domain.AuthSession, error) {
	return f.IssueMaxSession(context.Background(), account, session)
}

type fakeReader struct {
	pair  SessionAccount
	err   error
	calls int
}

func (f *fakeReader) FindSessionByTokenHash(context.Context, [32]byte) (SessionAccount, error) {
	f.calls++
	return f.pair, f.err
}

type memoryCache struct {
	values map[[32]byte]SessionAccount
}

func (c *memoryCache) Get(key [32]byte) (SessionAccount, bool) {
	value, ok := c.values[key]
	return value, ok
}
func (c *memoryCache) Set(key [32]byte, value SessionAccount, _ time.Duration) bool {
	c.values[key] = value
	return true
}
func (c *memoryCache) Delete(key [32]byte) { delete(c.values, key) }
func (*memoryCache) Close()                {}

func TestServiceExchangesInitDataForOpaqueSession(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	verifier, err := NewInitDataVerifier("bot-token", time.Hour, time.Minute, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	issuer := &fakeIssuer{}
	reader := &fakeReader{}
	random := bytes.NewReader(bytes.Repeat([]byte{7}, 64))
	service, err := NewService(verifier, issuer, reader, &memoryCache{values: map[[32]byte]SessionAccount{}}, ServiceConfig{
		SessionTTL: 24 * time.Hour,
		CacheTTL:   time.Minute,
		Clock:      func() time.Time { return now },
		Random:     random,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := signedInitData(map[string]string{
		"auth_date": "1790424000",
		"user":      `{"id":67890}`,
	}, "bot-token")
	issued, err := service.ExchangeMax(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(issued.AccessToken) != 43 || issued.Account.MaxUserID != "67890" {
		t.Fatalf("unexpected issued session: %+v", issued)
	}
	if issuer.session.TokenHash == ([32]byte{}) || issuer.session.IssuedVia != domain.SessionFromMax {
		t.Fatalf("unexpected stored session: %+v", issuer.session)
	}
	if !issuer.session.ExpiresAt.Equal(now.Add(24 * time.Hour)) {
		t.Fatalf("expires at %v", issuer.session.ExpiresAt)
	}
}

func TestServiceAuthenticatesThroughCache(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	token, hash, err := newToken(bytes.NewReader(bytes.Repeat([]byte{9}, tokenBytes)))
	if err != nil {
		t.Fatal(err)
	}
	userID := domain.UserID{1}
	pair := SessionAccount{
		Account: domain.UserAccount{ID: userID, MaxUserID: "1", State: domain.AccountActive, Kind: domain.AccountMax, CreatedAt: now, LastSeenAt: now},
		Session: domain.AuthSession{ID: domain.SessionID{2}, UserID: userID, TokenHash: hash, IssuedVia: domain.SessionFromMax, CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
	}
	reader := &fakeReader{pair: pair}
	verifier, _ := NewInitDataVerifier("token", time.Hour, time.Minute, func() time.Time { return now })
	cache := &memoryCache{values: map[[32]byte]SessionAccount{}}
	service, err := NewService(verifier, &fakeIssuer{}, reader, cache, ServiceConfig{
		SessionTTL: time.Hour,
		CacheTTL:   time.Minute,
		Clock:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if reader.calls != 1 {
		t.Fatalf("database reads = %d", reader.calls)
	}

	if _, err := service.Authenticate(context.Background(), "invalid"); !errors.Is(err, ErrAuthRequired) {
		t.Fatalf("invalid token error = %v", err)
	}
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	last := strings.IndexByte(alphabet, token[len(token)-1])
	alias := token[:len(token)-1] + string(alphabet[last+1])
	if _, err := service.Authenticate(context.Background(), alias); !errors.Is(err, ErrAuthRequired) {
		t.Fatalf("non-canonical token error = %v", err)
	}
	if reader.calls != 1 {
		t.Fatal("invalid token reached database")
	}
}

func TestServiceRejectsInvalidCachedSession(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	token, hash, _ := newToken(bytes.NewReader(bytes.Repeat([]byte{3}, tokenBytes)))
	userID := domain.UserID{1}
	pair := SessionAccount{
		Account: domain.UserAccount{ID: userID, MaxUserID: "1", State: domain.AccountDisabled, Kind: domain.AccountMax, CreatedAt: now, LastSeenAt: now},
		Session: domain.AuthSession{ID: domain.SessionID{2}, UserID: userID, TokenHash: hash, IssuedVia: domain.SessionFromMax, CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
	}
	cache := &memoryCache{values: map[[32]byte]SessionAccount{hash: pair}}
	verifier, _ := NewInitDataVerifier("token", time.Hour, time.Minute, func() time.Time { return now })
	service, _ := NewService(verifier, &fakeIssuer{}, &fakeReader{}, cache, ServiceConfig{SessionTTL: time.Hour, CacheTTL: time.Minute, Clock: func() time.Time { return now }})
	if _, err := service.Authenticate(context.Background(), token); !errors.Is(err, ErrAuthRequired) {
		t.Fatalf("error = %v", err)
	}
	if _, ok := cache.Get(hash); ok {
		t.Fatal("invalid cache entry retained")
	}
}
