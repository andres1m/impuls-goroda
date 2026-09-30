package postgres

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
)

func TestScheduleIntegration(t *testing.T) {
	f := newMaterializeFixture(t)
	schedule := NewSchedule(func() *pgxpool.Pool { return f.pool })

	if _, known, err := schedule.LastAttempt(f.ctx, f.source, domain.Perm); err != nil || known {
		t.Fatalf("attempt before any run: known %v, err %v", known, err)
	}
	attemptAt := time.Now().UTC().Truncate(time.Microsecond)
	if err := f.landing.FinishRun(f.ctx, &ingest.Run{
		SourceID: f.sourceID, City: domain.Perm, AttemptAt: attemptAt, ErrorCode: "timeout",
	}); err != nil {
		t.Fatal(err)
	}
	if at, known, err := schedule.LastAttempt(f.ctx, f.source, domain.Perm); err != nil || !known || !at.Equal(attemptAt) {
		t.Fatalf("a failed run is an attempt too: %v %v %v, want %v", at, known, err, attemptAt)
	}

	release, locked, err := schedule.TryLock(f.ctx, f.source, domain.Perm)
	if err != nil || !locked {
		t.Fatalf("first lock: %v, %v", locked, err)
	}
	if _, second, err := schedule.TryLock(f.ctx, f.source, domain.Perm); err != nil || second {
		t.Fatalf("a held lock was granted again: %v, %v", second, err)
	}
	releaseOther, other, err := schedule.TryLock(f.ctx, f.source, domain.Moscow)
	if err != nil || !other {
		t.Fatalf("locks of other cities are independent: %v, %v", other, err)
	}
	releaseOther()
	release()
	again, locked, err := schedule.TryLock(f.ctx, f.source, domain.Perm)
	if err != nil || !locked {
		t.Fatalf("lock after release: %v, %v", locked, err)
	}
	again()
}
