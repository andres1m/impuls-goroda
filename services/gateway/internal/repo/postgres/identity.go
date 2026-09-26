package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/jackc/pgx/v5/pgtype"
)

const upsertMaxAccountSQL = `
INSERT INTO identity.user_account (
    id, max_user_id, created_at, last_seen_at, account_state, account_kind
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (max_user_id) DO UPDATE
SET last_seen_at = GREATEST(user_account.last_seen_at, EXCLUDED.last_seen_at)
RETURNING id, max_user_id, created_at, last_seen_at, account_state, account_kind`

const createSessionSQL = `
INSERT INTO identity.auth_session (
    id, user_id, token_hash, issued_via, platform, created_at, expires_at, revoked_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, user_id, token_hash, issued_via, platform, created_at, expires_at, revoked_at`

const findSessionSQL = `
SELECT
    s.id, s.user_id, s.token_hash, s.issued_via, s.platform,
    s.created_at, s.expires_at, s.revoked_at,
    a.id, a.max_user_id, a.created_at, a.last_seen_at, a.account_state, a.account_kind
FROM identity.auth_session AS s
JOIN identity.user_account AS a ON a.id = s.user_id
WHERE s.token_hash = $1`

const revokeSessionSQL = `
UPDATE identity.auth_session
SET revoked_at = COALESCE(revoked_at, $3)
WHERE id = $1 AND user_id = $2
RETURNING id, user_id, token_hash, issued_via, platform, created_at, expires_at, revoked_at`

const findTestAccountSQL = `
SELECT id, max_user_id, created_at, last_seen_at, account_state, account_kind
FROM identity.user_account
WHERE max_user_id = $1 AND account_kind = 'test'`

type SessionAccount struct {
	Session domain.AuthSession
	Account domain.UserAccount
}

func (q *Queries) UpsertMaxAccount(ctx context.Context, account domain.UserAccount) (domain.UserAccount, error) {
	if err := account.Validate(); err != nil {
		return domain.UserAccount{}, fmt.Errorf("validate MAX account: %w", err)
	}
	if account.Kind != domain.AccountMax {
		return domain.UserAccount{}, errors.New("MAX account kind is required")
	}

	stored, err := scanAccount(q.db.QueryRow(
		ctx,
		upsertMaxAccountSQL,
		encodeUUID([16]byte(account.ID)),
		account.MaxUserID,
		account.CreatedAt,
		account.LastSeenAt,
		account.State,
		account.Kind,
	))
	if err != nil {
		return domain.UserAccount{}, mapQueryError("upsert MAX account", err)
	}
	return stored, nil
}

func (q *Queries) CreateSession(ctx context.Context, session domain.AuthSession) (domain.AuthSession, error) {
	if err := session.Validate(); err != nil {
		return domain.AuthSession{}, fmt.Errorf("validate session: %w", err)
	}

	stored, err := scanSession(q.db.QueryRow(
		ctx,
		createSessionSQL,
		encodeUUID([16]byte(session.ID)),
		encodeUUID([16]byte(session.UserID)),
		session.TokenHash[:],
		session.IssuedVia,
		session.Platform,
		session.CreatedAt,
		session.ExpiresAt,
		session.RevokedAt,
	))
	if err != nil {
		return domain.AuthSession{}, mapQueryError("create session", err)
	}
	return stored, nil
}

func (q *Queries) FindSessionByTokenHash(ctx context.Context, tokenHash [32]byte) (SessionAccount, error) {
	if tokenHash == ([32]byte{}) {
		return SessionAccount{}, errors.New("token hash is required")
	}

	pair, err := scanSessionAccount(q.db.QueryRow(ctx, findSessionSQL, tokenHash[:]))
	if err != nil {
		return SessionAccount{}, mapQueryError("find session", err)
	}
	return pair, nil
}

func (q *Queries) RevokeSession(
	ctx context.Context,
	sessionID domain.SessionID,
	userID domain.UserID,
	revokedAt time.Time,
) (domain.AuthSession, error) {
	if sessionID == (domain.SessionID{}) || userID == (domain.UserID{}) {
		return domain.AuthSession{}, errors.New("session and user identifiers are required")
	}
	if revokedAt.IsZero() {
		return domain.AuthSession{}, errors.New("revocation time is required")
	}

	stored, err := scanSession(q.db.QueryRow(
		ctx,
		revokeSessionSQL,
		encodeUUID([16]byte(sessionID)),
		encodeUUID([16]byte(userID)),
		revokedAt,
	))
	if err != nil {
		return domain.AuthSession{}, mapQueryError("revoke session", err)
	}
	return stored, nil
}

func (q *Queries) FindTestAccount(ctx context.Context, maxUserID string) (domain.UserAccount, error) {
	if !strings.HasPrefix(maxUserID, "test:") {
		return domain.UserAccount{}, errors.New("test account identifier is required")
	}

	account, err := scanAccount(q.db.QueryRow(ctx, findTestAccountSQL, maxUserID))
	if err != nil {
		return domain.UserAccount{}, mapQueryError("find test account", err)
	}
	return account, nil
}

func scanAccount(row interface{ Scan(...any) error }) (domain.UserAccount, error) {
	var id pgtype.UUID
	var account domain.UserAccount
	if err := row.Scan(
		&id,
		&account.MaxUserID,
		&account.CreatedAt,
		&account.LastSeenAt,
		&account.State,
		&account.Kind,
	); err != nil {
		return domain.UserAccount{}, err
	}
	decodedID, err := decodeUUID(id)
	if err != nil {
		return domain.UserAccount{}, err
	}
	account.ID = domain.UserID(decodedID)
	if err := account.Validate(); err != nil {
		return domain.UserAccount{}, fmt.Errorf("invalid stored account: %w", err)
	}
	return account, nil
}

func scanSession(row interface{ Scan(...any) error }) (domain.AuthSession, error) {
	var session domain.AuthSession
	var id, userID pgtype.UUID
	var tokenHash []byte
	var platform pgtype.Text
	var revokedAt pgtype.Timestamptz
	if err := row.Scan(
		&id,
		&userID,
		&tokenHash,
		&session.IssuedVia,
		&platform,
		&session.CreatedAt,
		&session.ExpiresAt,
		&revokedAt,
	); err != nil {
		return domain.AuthSession{}, err
	}
	if err := populateSession(&session, id, userID, tokenHash, platform, revokedAt); err != nil {
		return domain.AuthSession{}, err
	}
	return session, nil
}

func scanSessionAccount(row interface{ Scan(...any) error }) (SessionAccount, error) {
	var pair SessionAccount
	var sessionID, sessionUserID, accountID pgtype.UUID
	var tokenHash []byte
	var platform pgtype.Text
	var revokedAt pgtype.Timestamptz
	if err := row.Scan(
		&sessionID,
		&sessionUserID,
		&tokenHash,
		&pair.Session.IssuedVia,
		&platform,
		&pair.Session.CreatedAt,
		&pair.Session.ExpiresAt,
		&revokedAt,
		&accountID,
		&pair.Account.MaxUserID,
		&pair.Account.CreatedAt,
		&pair.Account.LastSeenAt,
		&pair.Account.State,
		&pair.Account.Kind,
	); err != nil {
		return SessionAccount{}, err
	}
	if err := populateSession(&pair.Session, sessionID, sessionUserID, tokenHash, platform, revokedAt); err != nil {
		return SessionAccount{}, err
	}
	decodedAccountID, err := decodeUUID(accountID)
	if err != nil {
		return SessionAccount{}, err
	}
	pair.Account.ID = domain.UserID(decodedAccountID)
	if err := pair.Account.Validate(); err != nil {
		return SessionAccount{}, fmt.Errorf("invalid stored account: %w", err)
	}
	if pair.Session.UserID != pair.Account.ID {
		return SessionAccount{}, errors.New("stored session and account disagree")
	}
	return pair, nil
}

func populateSession(
	session *domain.AuthSession,
	id pgtype.UUID,
	userID pgtype.UUID,
	tokenHash []byte,
	platform pgtype.Text,
	revokedAt pgtype.Timestamptz,
) error {
	decodedID, err := decodeUUID(id)
	if err != nil {
		return err
	}
	decodedUserID, err := decodeUUID(userID)
	if err != nil {
		return err
	}
	decodedHash, err := decodeHash(tokenHash)
	if err != nil {
		return err
	}
	session.ID = domain.SessionID(decodedID)
	session.UserID = domain.UserID(decodedUserID)
	session.TokenHash = decodedHash
	if platform.Valid {
		value := platform.String
		session.Platform = &value
	}
	if revokedAt.Valid {
		value := revokedAt.Time
		session.RevokedAt = &value
	}
	if err := session.Validate(); err != nil {
		return fmt.Errorf("invalid stored session: %w", err)
	}
	return nil
}
