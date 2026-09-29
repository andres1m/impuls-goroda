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
	db                     DBTX
	maxDeliveryEvents      bool
	scenarioResultDelivery bool
}

func NewQueries(db DBTX) (*Queries, error) {
	if db == nil {
		return nil, errors.New("database connection is required")
	}
	return &Queries{db: db}, nil
}

type Transactor struct {
	pool                   *pgxpool.Pool
	maxDeliveryEvents      bool
	scenarioResultDelivery bool
}

func (t *Transactor) EnableMAXDeliveryEvents(enabled bool) { t.maxDeliveryEvents = enabled }

func (t *Transactor) EnableScenarioResultDelivery(enabled bool) { t.scenarioResultDelivery = enabled }

func NewTransactor(pool *pgxpool.Pool) (*Transactor, error) {
	if pool == nil {
		return nil, errors.New("database pool is required")
	}
	return &Transactor{pool: pool}, nil
}

func (t *Transactor) WithinTx(
	ctx context.Context,
	options *pgx.TxOptions,
	fn func(*Queries) error,
) (err error) {
	if fn == nil {
		return errors.New("transaction callback is required")
	}

	var txOptions pgx.TxOptions
	if options != nil {
		txOptions = *options
	}
	tx, err := t.pool.BeginTx(ctx, txOptions)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil &&
			!errors.Is(rollbackErr, pgx.ErrTxClosed) && err == nil {
			err = fmt.Errorf("rollback transaction: %w", rollbackErr)
		}
	}()

	queries, err := NewQueries(tx)
	if err != nil {
		return err
	}
	queries.maxDeliveryEvents = t.maxDeliveryEvents
	queries.scenarioResultDelivery = t.scenarioResultDelivery
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
	account *domain.UserAccount,
	session *domain.AuthSession,
) (domain.UserAccount, domain.AuthSession, error) {
	if account == nil || session == nil ||
		account.Kind != domain.AccountMax || session.IssuedVia != domain.SessionFromMax {
		return domain.UserAccount{}, domain.AuthSession{}, errors.New("MAX account and session issuer are required")
	}
	return t.issueSession(ctx, account, session, (*Queries).UpsertMaxAccount)
}

func (t *Transactor) IssueTestSession(
	ctx context.Context,
	account *domain.UserAccount,
	session *domain.AuthSession,
) (domain.UserAccount, domain.AuthSession, error) {
	if account == nil || session == nil ||
		account.Kind != domain.AccountTest || session.IssuedVia != domain.SessionFromTest {
		return domain.UserAccount{}, domain.AuthSession{}, errors.New("test account and session issuer are required")
	}
	return t.issueSession(ctx, account, session, (*Queries).UpsertTestAccount)
}

func (t *Transactor) issueSession(
	ctx context.Context,
	account *domain.UserAccount,
	session *domain.AuthSession,
	upsert func(*Queries, context.Context, *domain.UserAccount) (domain.UserAccount, error),
) (domain.UserAccount, domain.AuthSession, error) {
	var storedAccount domain.UserAccount
	var storedSession domain.AuthSession
	err := t.WithinTx(ctx, nil, func(queries *Queries) error {
		var upsertErr error
		storedAccount, upsertErr = upsert(queries, ctx, account)
		if upsertErr != nil {
			return upsertErr
		}
		if storedAccount.State != domain.AccountActive {
			return ErrAccountDisabled
		}
		sessionCopy := *session
		sessionCopy.UserID = storedAccount.ID
		var createErr error
		storedSession, createErr = queries.CreateSession(ctx, &sessionCopy)
		return createErr
	})
	if err != nil {
		return domain.UserAccount{}, domain.AuthSession{}, fmt.Errorf("issue session: %w", err)
	}
	return storedAccount, storedSession, nil
}
