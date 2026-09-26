package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type Queries struct {
	db DBTX
}

func NewQueries(db DBTX) (*Queries, error) {
	if db == nil {
		return nil, errors.New("database connection is required")
	}
	return &Queries{db: db}, nil
}

type Transactor struct {
	pool *pgxpool.Pool
}

func NewTransactor(pool *pgxpool.Pool) (*Transactor, error) {
	if pool == nil {
		return nil, errors.New("database pool is required")
	}
	return &Transactor{pool: pool}, nil
}

func (t *Transactor) WithinTx(
	ctx context.Context,
	options pgx.TxOptions,
	fn func(*Queries) error,
) error {
	if fn == nil {
		return errors.New("transaction callback is required")
	}

	tx, err := t.pool.BeginTx(ctx, options)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	queries, err := NewQueries(tx)
	if err != nil {
		return err
	}
	if err := fn(queries); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func (t *Transactor) IssueMaxSession(
	ctx context.Context,
	account domain.UserAccount,
	session domain.AuthSession,
) (domain.UserAccount, domain.AuthSession, error) {
	if account.Kind != domain.AccountMax || session.IssuedVia != domain.SessionFromMax {
		return domain.UserAccount{}, domain.AuthSession{}, errors.New("MAX account and session issuer are required")
	}
	return t.issueSession(ctx, account, session, (*Queries).UpsertMaxAccount)
}

func (t *Transactor) IssueTestSession(
	ctx context.Context,
	account domain.UserAccount,
	session domain.AuthSession,
) (domain.UserAccount, domain.AuthSession, error) {
	if account.Kind != domain.AccountTest || session.IssuedVia != domain.SessionFromTest {
		return domain.UserAccount{}, domain.AuthSession{}, errors.New("test account and session issuer are required")
	}
	return t.issueSession(ctx, account, session, (*Queries).UpsertTestAccount)
}

func (t *Transactor) issueSession(
	ctx context.Context,
	account domain.UserAccount,
	session domain.AuthSession,
	upsert func(*Queries, context.Context, domain.UserAccount) (domain.UserAccount, error),
) (domain.UserAccount, domain.AuthSession, error) {
	var storedAccount domain.UserAccount
	var storedSession domain.AuthSession
	err := t.WithinTx(ctx, pgx.TxOptions{}, func(queries *Queries) error {
		var err error
		storedAccount, err = upsert(queries, ctx, account)
		if err != nil {
			return err
		}
		if storedAccount.State != domain.AccountActive {
			return ErrAccountDisabled
		}
		session.UserID = storedAccount.ID
		storedSession, err = queries.CreateSession(ctx, session)
		return err
	})
	if err != nil {
		return domain.UserAccount{}, domain.AuthSession{}, fmt.Errorf("issue session: %w", err)
	}
	return storedAccount, storedSession, nil
}
