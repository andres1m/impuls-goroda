package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

const unlockTimeout = 5 * time.Second

// Schedule is the database side of collection scheduling. It reaches the pool through a function: the
// pool exists only once the database component has started.
type Schedule struct {
	pool func() *pgxpool.Pool
}

func NewSchedule(pool func() *pgxpool.Pool) *Schedule {
	return &Schedule{pool: pool}
}

func (s *Schedule) connected() (*pgxpool.Pool, error) {
	if p := s.pool(); p != nil {
		return p, nil
	}
	return nil, errNotConnected
}

// LastAttempt returns when the source's collection for the city was last tried, successful or not; known
// is false when it never was.
func (s *Schedule) LastAttempt(ctx context.Context, source domain.SourceKey, city domain.City) (
	at time.Time, known bool, err error,
) {
	pool, err := s.connected()
	if err != nil {
		return time.Time{}, false, err
	}
	var last *time.Time
	err = pool.QueryRow(ctx, `
		SELECT c.last_attempt_at FROM integration.sync_cursor c
		JOIN integration.source src ON src.id = c.source_id
		WHERE src.source_key = $1 AND c.city = $2`, source, city).Scan(&last)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && last == nil) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("read last attempt of %s %s: %w", source, city, err)
	}
	return *last, true, nil
}

// TryLock takes the right to collect the source for the city, so that a scheduled run and a manual one
// do not overlap. The lock belongs to the connection it is taken on and is given up by release, or when
// that connection ends.
func (s *Schedule) TryLock(ctx context.Context, source domain.SourceKey, city domain.City) (
	release func(), locked bool, err error,
) {
	pool, err := s.connected()
	if err != nil {
		return nil, false, err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("acquire connection: %w", err)
	}
	key := "ingest:" + string(source) + ":" + string(city)
	if scanErr := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, key).
		Scan(&locked); scanErr != nil || !locked {
		if scanErr != nil {
			// The lock request may have reached the server: the connection must not go back to the pool.
			_ = conn.Conn().Close(ctx)
			conn.Release()
			return nil, false, fmt.Errorf("lock %s: %w", key, scanErr)
		}
		conn.Release()
		return nil, false, nil
	}
	return func() {
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), unlockTimeout)
		defer cancel()
		var unlocked bool
		if unlockErr := conn.QueryRow(unlockCtx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, key).
			Scan(&unlocked); unlockErr != nil || !unlocked {
			// A connection that keeps the lock must not go back to the pool.
			_ = conn.Conn().Close(unlockCtx)
		}
		conn.Release()
	}, true, nil
}
