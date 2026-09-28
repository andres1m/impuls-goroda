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
	beginErr error
}

func (q stubQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return q.queryRow(ctx, sql, args...)
}

func (q stubQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return q.query(ctx, sql, args...)
}

func (q stubQuerier) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	if q.beginErr != nil {
		return nil, q.beginErr
	}
	return stubTx{q: q}, nil
}

// stubTx runs the snapshot's queries on the stub querier.
type stubTx struct {
	pgx.Tx
	q stubQuerier
}

func (t stubTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return t.q.QueryRow(ctx, sql, args...)
}

func (t stubTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return t.q.Query(ctx, sql, args...)
}

func (stubTx) Rollback(context.Context) error { return nil }

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
			query: func(context.Context, string, ...any) (pgx.Rows, error) { return &emptyRows{}, nil },
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

	t.Run("snapshot that cannot start wraps ErrUnavailable", func(t *testing.T) {
		c := NewCatalog(stubQuerier{beginErr: errors.New("too many connections")})
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

// snapshotProbe fails any query made outside the snapshot transaction and records how it was opened.
type snapshotProbe struct {
	opts   pgx.TxOptions
	inside stubQuerier
	direct int
}

func (p *snapshotProbe) Query(context.Context, string, ...any) (pgx.Rows, error) {
	p.direct++
	return nil, errors.New("query outside the snapshot")
}

func (p *snapshotProbe) QueryRow(context.Context, string, ...any) pgx.Row {
	p.direct++
	return stubRow{scan: func(...any) error { return errors.New("query outside the snapshot") }}
}

func (p *snapshotProbe) BeginTx(_ context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	p.opts = opts
	return stubTx{q: p.inside}, nil
}

func TestLoadSliceReadsOneSnapshot(t *testing.T) {
	updatedAt := time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC)
	probe := &snapshotProbe{inside: stubQuerier{
		queryRow: func(context.Context, string, ...any) pgx.Row {
			return stubRow{scan: func(dest ...any) error {
				*(dest[0].(*string)) = "Asia/Yekaterinburg"
				*(dest[1].(*int64)) = 9
				*(dest[2].(*time.Time)) = updatedAt
				return nil
			}}
		},
		query: func(context.Context, string, ...any) (pgx.Rows, error) { return &emptyRows{}, nil },
	}}
	horizon := time.Date(2026, 9, 27, 8, 0, 0, 0, time.UTC)
	slice, err := NewCatalog(probe).LoadSlice(context.Background(), "perm", horizon)
	if err != nil {
		t.Fatal(err)
	}
	if probe.direct != 0 {
		t.Fatalf("%d queries ran outside the snapshot", probe.direct)
	}
	if probe.opts.IsoLevel != pgx.RepeatableRead || probe.opts.AccessMode != pgx.ReadOnly {
		t.Fatalf("snapshot opened with %+v", probe.opts)
	}
	if slice.Revision != 9 || slice.Timezone != "Asia/Yekaterinburg" || !slice.Horizon.Equal(horizon) || !slice.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("slice %+v", slice)
	}
}

func TestRevision(t *testing.T) {
	row := func(revision int64, err error) stubQuerier {
		return stubQuerier{queryRow: func(context.Context, string, ...any) pgx.Row {
			return stubRow{scan: func(dest ...any) error {
				*(dest[0].(*int64)) = revision
				return err
			}}
		}}
	}
	if got, err := NewCatalog(row(4, nil)).Revision(context.Background(), "perm"); err != nil || got != 4 {
		t.Fatalf("revision %d, %v", got, err)
	}
	for name, tc := range map[string]struct {
		db   stubQuerier
		want error
	}{
		"no city":     {row(0, pgx.ErrNoRows), usecase.ErrCatalogNotReady},
		"unpublished": {row(0, nil), usecase.ErrCatalogNotReady},
		"db down":     {row(0, errors.New("connection refused")), usecase.ErrUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewCatalog(tc.db).Revision(context.Background(), "perm"); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
	if _, err := NewCatalog(nil).Revision(context.Background(), "perm"); !errors.Is(err, usecase.ErrUnavailable) {
		t.Fatalf("no database: %v", err)
	}
}
