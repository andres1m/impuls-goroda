package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

func TestSaveBoundaryIntegration(t *testing.T) {
	pool := outboxPool(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // the test leaves the stored boundary as it was

	halves := [][][2]float64{
		{{56.0, 57.9}, {56.5, 57.9}, {56.5, 58.1}},
		{{56.5, 58.1}, {56.0, 58.1}, {56.0, 57.9}},
	}
	stats, err := SaveBoundary(ctx, tx, domain.Perm, halves, time.Now())
	if err != nil || stats.Parts != 1 || stats.AreaKm2 < 600 || stats.AreaKm2 > 700 {
		t.Fatalf("stats %+v, err %v", stats, err)
	}
	covers := func() bool {
		var covered bool
		if err := tx.QueryRow(ctx, `SELECT ST_Covers(boundary, ST_SetSRID(ST_MakePoint(56.25, 58.0), 4326))
			FROM ref.city WHERE code = 'perm'`).Scan(&covered); err != nil {
			t.Fatal(err)
		}
		return covered
	}
	if !covers() {
		t.Fatal("the centre is outside the saved boundary")
	}
	open := [][][2]float64{{{56.0, 57.9}, {56.5, 57.9}}}
	if _, err := SaveBoundary(ctx, tx, domain.Perm, open, time.Now()); err == nil {
		t.Fatal("an open line became a boundary")
	}
	if !covers() {
		t.Fatal("a refused boundary replaced the stored one")
	}
}
