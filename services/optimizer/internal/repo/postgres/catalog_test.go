package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/usecase"
)

type stubRow struct {
	scan func(dest ...any) error
}

func (r stubRow) Scan(dest ...any) error { return r.scan(dest...) }

type stubQuerier struct {
	queryRow func(ctx context.Context, sql string, args ...any) pgx.Row
	query    func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func (q stubQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return q.queryRow(ctx, sql, args...)
}

func (q stubQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return q.query(ctx, sql, args...)
}

type emptyRows struct {
	pgx.Rows
	err error
}

func (r *emptyRows) Close()                        {}
func (r *emptyRows) Err() error                    { return r.err }
func (r *emptyRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }
func (r *emptyRows) Next() bool                    { return false }

func validOptimizeRequest() domain.OptimizeRequest {
	loc, _ := time.LoadLocation("Asia/Yekaterinburg")
	return domain.OptimizeRequest{
		City:     "perm",
		Timezone: "Asia/Yekaterinburg",
		Start:    time.Date(2026, 9, 28, 10, 0, 0, 0, loc).UTC(),
		End:      time.Date(2026, 9, 28, 18, 0, 0, 0, loc).UTC(),
		Origin:   permCenter,
		Constraints: domain.RouteConstraints{
			MovementModes: []domain.MovementMode{domain.MovementWalk, domain.MovementTransit},
			LoadProfile:   "moderate",
			Budget:        domain.Budget{Mode: domain.BudgetNone},
		},
	}
}

func TestCatalogCandidatesRejectsInvalidRequest(t *testing.T) {
	c := NewCatalog(nil)
	req := validOptimizeRequest()
	req.City = ""
	if _, _, err := c.Candidates(context.Background(), req); !errors.Is(err, usecase.ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

func TestCatalogCandidatesCityErrors(t *testing.T) {
	updatedAt := time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC)

	t.Run("missing city returns ErrCatalogNotReady", func(t *testing.T) {
		c := NewCatalog(stubQuerier{
			queryRow: func(context.Context, string, ...any) pgx.Row {
				return stubRow{scan: func(...any) error { return pgx.ErrNoRows }}
			},
		})
		if _, _, err := c.Candidates(context.Background(), validOptimizeRequest()); !errors.Is(err, usecase.ErrCatalogNotReady) {
			t.Fatalf("got %v, want ErrCatalogNotReady", err)
		}
	})

	t.Run("zero catalog revision returns ErrCatalogNotReady", func(t *testing.T) {
		c := NewCatalog(stubQuerier{
			queryRow: func(context.Context, string, ...any) pgx.Row {
				return stubRow{scan: func(dest ...any) error {
					*(dest[0].(*string)) = "Asia/Yekaterinburg"
					*(dest[1].(*int64)) = 0
					*(dest[2].(*time.Time)) = updatedAt
					return nil
				}}
			},
		})
		if _, _, err := c.Candidates(context.Background(), validOptimizeRequest()); !errors.Is(err, usecase.ErrCatalogNotReady) {
			t.Fatalf("got %v, want ErrCatalogNotReady", err)
		}
	})

	t.Run("timezone mismatch returns ErrInvalidRequest", func(t *testing.T) {
		c := NewCatalog(stubQuerier{
			queryRow: func(context.Context, string, ...any) pgx.Row {
				return stubRow{scan: func(dest ...any) error {
					*(dest[0].(*string)) = "Europe/Moscow"
					*(dest[1].(*int64)) = 3
					*(dest[2].(*time.Time)) = updatedAt
					return nil
				}}
			},
		})
		if _, _, err := c.Candidates(context.Background(), validOptimizeRequest()); !errors.Is(err, usecase.ErrInvalidRequest) {
			t.Fatalf("got %v, want ErrInvalidRequest", err)
		}
	})

	t.Run("database failure wraps ErrUnavailable", func(t *testing.T) {
		c := NewCatalog(stubQuerier{
			queryRow: func(context.Context, string, ...any) pgx.Row {
				return stubRow{scan: func(...any) error { return errors.New("connection refused") }}
			},
		})
		if _, _, err := c.Candidates(context.Background(), validOptimizeRequest()); !errors.Is(err, usecase.ErrUnavailable) {
			t.Fatalf("got %v, want ErrUnavailable", err)
		}
	})

	t.Run("canceled context preserves context.Canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c := NewCatalog(stubQuerier{
			queryRow: func(context.Context, string, ...any) pgx.Row {
				return stubRow{scan: func(...any) error { return context.Canceled }}
			},
		})
		if _, _, err := c.Candidates(ctx, validOptimizeRequest()); !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
	})

	t.Run("empty active catalog returns valid DataFreshness", func(t *testing.T) {
		c := NewCatalog(stubQuerier{
			queryRow: func(context.Context, string, ...any) pgx.Row {
				return stubRow{scan: func(dest ...any) error {
					*(dest[0].(*string)) = "Asia/Yekaterinburg"
					*(dest[1].(*int64)) = 5
					*(dest[2].(*time.Time)) = updatedAt
					return nil
				}}
			},
			query: func(context.Context, string, ...any) (pgx.Rows, error) {
				return &emptyRows{}, nil
			},
		})
		candidates, freshness, err := c.Candidates(context.Background(), validOptimizeRequest())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(candidates) != 0 {
			t.Fatalf("expected 0 candidates, got %d", len(candidates))
		}
		if err := freshness.Validate(); err != nil {
			t.Fatalf("invalid freshness: %v", err)
		}
		if freshness.CatalogRevision != 5 || freshness.DataAsOf == nil || !freshness.DataAsOf.Equal(updatedAt) {
			t.Fatalf("unexpected freshness: %+v", freshness)
		}
	})
}
