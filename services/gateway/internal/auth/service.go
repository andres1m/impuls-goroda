package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
)

var ErrAuthRequired = errors.New("authentication required")

type SessionAccount struct {
	Session domain.AuthSession
	Account domain.UserAccount
}

type SessionIssuer interface {
	IssueMaxSession(context.Context, domain.UserAccount, domain.AuthSession) (domain.UserAccount, domain.AuthSession, error)
	IssueTestSession(context.Context, domain.UserAccount, domain.AuthSession) (domain.UserAccount, domain.AuthSession, error)
}

type SessionReader interface {
	FindSessionByTokenHash(context.Context, [32]byte) (SessionAccount, error)
}

type ServiceConfig struct {
	SessionTTL time.Duration
	CacheTTL   time.Duration
	Clock      func() time.Time
	Random     io.Reader
}

type Service struct {
	verifier *InitDataVerifier
	issuer   SessionIssuer
	reader   SessionReader
	cache    SessionCache
	clock    func() time.Time
	random   io.Reader
	ttl      time.Duration
	cacheTTL time.Duration
}

type IssuedSession struct {
	AccessToken string
	ExpiresAt   time.Time
	Account     domain.UserAccount
}

func NewService(
	verifier *InitDataVerifier,
	issuer SessionIssuer,
	reader SessionReader,
	cache SessionCache,
	cfg ServiceConfig,
) (*Service, error) {
	if verifier == nil || issuer == nil || reader == nil || cache == nil {
		return nil, errors.New("auth dependencies are required")
	}
	if cfg.SessionTTL <= 0 || cfg.CacheTTL <= 0 {
		return nil, errors.New("auth lifetimes must be positive")
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.Random == nil {
		cfg.Random = rand.Reader
	}
	return &Service{
		verifier: verifier,
		issuer:   issuer,
		reader:   reader,
		cache:    cache,
		clock:    cfg.Clock,
		random:   cfg.Random,
		ttl:      cfg.SessionTTL,
		cacheTTL: cfg.CacheTTL,
	}, nil
}

func (s *Service) ExchangeMax(ctx context.Context, rawInitData string) (IssuedSession, error) {
	verified, err := s.verifier.Verify(rawInitData)
	if err != nil {
		return IssuedSession{}, ErrAuthRequired
	}
	now := s.clock().UTC()
	userID, err := newUUID(s.random)
	if err != nil {
		return IssuedSession{}, fmt.Errorf("generate user identifier: %w", err)
	}
	sessionID, err := newUUID(s.random)
	if err != nil {
		return IssuedSession{}, fmt.Errorf("generate session identifier: %w", err)
	}
	token, tokenHash, err := newToken(s.random)
	if err != nil {
		return IssuedSession{}, fmt.Errorf("generate session token: %w", err)
	}
	account := domain.UserAccount{
		ID:         domain.UserID(userID),
		MaxUserID:  verified.MaxUserID,
		State:      domain.AccountActive,
		Kind:       domain.AccountMax,
		CreatedAt:  now,
		LastSeenAt: now,
	}
	session := domain.AuthSession{
		ID:        domain.SessionID(sessionID),
		UserID:    account.ID,
		TokenHash: tokenHash,
		IssuedVia: domain.SessionFromMax,
		CreatedAt: now,
		ExpiresAt: now.Add(s.ttl),
	}
	storedAccount, storedSession, err := s.issuer.IssueMaxSession(ctx, account, session)
	if err != nil {
		return IssuedSession{}, fmt.Errorf("issue MAX session: %w", err)
	}
	return IssuedSession{AccessToken: token, ExpiresAt: storedSession.ExpiresAt, Account: storedAccount}, nil
}

func (s *Service) IssueTest(
	ctx context.Context,
	maxUserID string,
	expiresAt time.Time,
) (IssuedSession, error) {
	if !strings.HasPrefix(maxUserID, "test:") {
		return IssuedSession{}, errors.New("test account identifier is required")
	}
	now := s.clock().UTC()
	if !expiresAt.After(now) {
		return IssuedSession{}, errors.New("test session expiry must be in the future")
	}
	userID, err := newUUID(s.random)
	if err != nil {
		return IssuedSession{}, fmt.Errorf("generate user identifier: %w", err)
	}
	sessionID, err := newUUID(s.random)
	if err != nil {
		return IssuedSession{}, fmt.Errorf("generate session identifier: %w", err)
	}
	token, tokenHash, err := newToken(s.random)
	if err != nil {
		return IssuedSession{}, fmt.Errorf("generate session token: %w", err)
	}
	account := domain.UserAccount{
		ID:         domain.UserID(userID),
		MaxUserID:  maxUserID,
		State:      domain.AccountActive,
		Kind:       domain.AccountTest,
		CreatedAt:  now,
		LastSeenAt: now,
	}
	session := domain.AuthSession{
		ID:        domain.SessionID(sessionID),
		UserID:    account.ID,
		TokenHash: tokenHash,
		IssuedVia: domain.SessionFromTest,
		CreatedAt: now,
		ExpiresAt: expiresAt.UTC(),
	}
	storedAccount, storedSession, err := s.issuer.IssueTestSession(ctx, account, session)
	if err != nil {
		return IssuedSession{}, fmt.Errorf("issue test session: %w", err)
	}
	return IssuedSession{AccessToken: token, ExpiresAt: storedSession.ExpiresAt, Account: storedAccount}, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (SessionAccount, error) {
	hash, err := hashToken(token)
	if err != nil {
		return SessionAccount{}, ErrAuthRequired
	}
	now := s.clock().UTC()
	if cached, ok := s.cache.Get(hash); ok {
		if cached.Session.ValidFor(cached.Account, now) {
			return cached, nil
		}
		s.cache.Delete(hash)
		return SessionAccount{}, ErrAuthRequired
	}
	pair, err := s.reader.FindSessionByTokenHash(ctx, hash)
	if err != nil {
		return SessionAccount{}, err
	}
	if !pair.Session.ValidFor(pair.Account, now) {
		return SessionAccount{}, ErrAuthRequired
	}
	ttl := min(s.cacheTTL, pair.Session.ExpiresAt.Sub(now))
	if ttl > 0 {
		s.cache.Set(hash, pair, ttl)
	}
	return pair, nil
}

func (s *Service) Close() {
	s.cache.Close()
}
