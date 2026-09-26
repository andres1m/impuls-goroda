package domain

import (
	"errors"
	"strings"
	"time"
)

type AccountState string

const (
	AccountActive   AccountState = "active"
	AccountDisabled AccountState = "disabled"
)

type AccountKind string

const (
	AccountMax  AccountKind = "max"
	AccountTest AccountKind = "test"
)

type UserAccount struct {
	ID         UserID
	MaxUserID  string
	State      AccountState
	Kind       AccountKind
	CreatedAt  time.Time
	LastSeenAt time.Time
}

func (u UserAccount) Validate() error {
	if err := requiredID([16]byte(u.ID)); err != nil {
		return err
	}
	if u.MaxUserID == "" {
		return errors.New("MAX user identifier is required")
	}
	if u.State != AccountActive && u.State != AccountDisabled {
		return errors.New("invalid account state")
	}
	if u.Kind != AccountMax && u.Kind != AccountTest {
		return errors.New("invalid account kind")
	}
	if (u.Kind == AccountTest) != strings.HasPrefix(u.MaxUserID, "test:") {
		return errors.New("account kind and identifier namespace disagree")
	}
	if u.CreatedAt.IsZero() || u.LastSeenAt.IsZero() {
		return errors.New("account timestamps are required")
	}
	return nil
}

type SessionIssuer string

const (
	SessionFromMax  SessionIssuer = "max_init_data"
	SessionFromTest SessionIssuer = "test_cli"
)

type AuthSession struct {
	ID        SessionID
	UserID    UserID
	TokenHash [32]byte
	IssuedVia SessionIssuer
	Platform  *string
	CreatedAt time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
}

func (s AuthSession) Validate() error {
	if err := requiredID([16]byte(s.ID)); err != nil {
		return err
	}
	if err := requiredID([16]byte(s.UserID)); err != nil {
		return err
	}
	if s.TokenHash == ([32]byte{}) {
		return errors.New("session token hash is required")
	}
	if s.IssuedVia != SessionFromMax && s.IssuedVia != SessionFromTest {
		return errors.New("invalid session issuer")
	}
	if s.CreatedAt.IsZero() || !s.ExpiresAt.After(s.CreatedAt) {
		return errors.New("invalid session lifetime")
	}
	return nil
}

func (s AuthSession) ValidFor(account UserAccount, now time.Time) bool {
	return s.Validate() == nil && account.Validate() == nil &&
		s.UserID == account.ID && account.State == AccountActive &&
		s.RevokedAt == nil && !now.Before(s.CreatedAt) && now.Before(s.ExpiresAt)
}
