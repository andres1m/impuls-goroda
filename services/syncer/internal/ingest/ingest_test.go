package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

var testSource = domain.Source{Key: domain.KudaGo, Name: "Test", AccessMode: domain.AccessAPI, SchemaVersion: "1", DataMode: domain.Live}

type fakeAdapter struct {
	batch     Batch
	err       error
	gotCursor json.RawMessage
}

func (a *fakeAdapter) Source() domain.Source { return testSource }

func (a *fakeAdapter) Fetch(_ context.Context, _ domain.City, cursor json.RawMessage) (Batch, error) {
	a.gotCursor = cursor
	return a.batch, a.err
}

type savedRecord struct {
	source    SourceID
	city      domain.City
	mode      domain.DataMode
	record    domain.RawRecord
	fetchedAt time.Time
}

type fakeLanding struct {
	sourceID  SourceID
	cursor    json.RawMessage
	unchanged map[string]bool
	saveErr   error
	ensured   []domain.Source
	saved     []savedRecord
	runs      []Run
}

func (l *fakeLanding) EnsureSource(_ context.Context, s domain.Source) (SourceID, error) {
	l.ensured = append(l.ensured, s)
	return l.sourceID, nil
}

func (l *fakeLanding) Cursor(context.Context, SourceID, domain.City) (json.RawMessage, error) {
	return l.cursor, nil
}

func (l *fakeLanding) SaveRecord(_ context.Context, source SourceID, city domain.City, mode domain.DataMode, record domain.RawRecord, fetchedAt time.Time) (bool, error) {
	if l.saveErr != nil {
		return false, l.saveErr
	}
	l.saved = append(l.saved, savedRecord{source, city, mode, record, fetchedAt})
	return !l.unchanged[record.ExternalID], nil
}

func (l *fakeLanding) FinishRun(_ context.Context, run Run) error {
	l.runs = append(l.runs, run)
	return nil
}

var fixedNow = time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)

func newTestService(l *fakeLanding) *Service {
	return NewService(l, func() time.Time { return fixedNow })
}

func TestIngestSavesRecordsAndCursor(t *testing.T) {
	landing := &fakeLanding{sourceID: SourceID{1}, unchanged: map[string]bool{"event:2": true}}
	adapter := &fakeAdapter{batch: Batch{
		Records: []domain.RawRecord{{ExternalID: "event:1"}, {ExternalID: "event:2"}},
		Skipped: 3,
		Cursor:  json.RawMessage(`{"snapshot_at":"2026-09-27T10:00:00Z"}`),
	}}

	result, err := newTestService(landing).Ingest(context.Background(), adapter, domain.Perm)
	if err != nil {
		t.Fatal(err)
	}
	if result != (Result{Received: 2, Inserted: 1, Skipped: 3}) {
		t.Fatalf("result = %+v", result)
	}
	if len(landing.ensured) != 1 || landing.ensured[0] != testSource {
		t.Fatalf("ensured = %+v", landing.ensured)
	}
	for _, s := range landing.saved {
		if s.source != (SourceID{1}) || s.city != domain.Perm || s.mode != domain.Live || !s.fetchedAt.Equal(fixedNow) {
			t.Fatalf("saved with wrong context: %+v", s)
		}
	}
	want := Run{SourceID: SourceID{1}, City: domain.Perm, AttemptAt: fixedNow, Cursor: adapter.batch.Cursor}
	if len(landing.runs) != 1 || !sameRun(landing.runs[0], want) {
		t.Fatalf("runs = %+v", landing.runs)
	}
}

func TestIngestPassesStoredCursorToAdapter(t *testing.T) {
	landing := &fakeLanding{cursor: json.RawMessage(`{"dataset_version":12}`)}
	adapter := &fakeAdapter{}

	if _, err := newTestService(landing).Ingest(context.Background(), adapter, domain.Moscow); err != nil {
		t.Fatal(err)
	}
	if string(adapter.gotCursor) != `{"dataset_version":12}` {
		t.Fatalf("adapter got cursor %s", adapter.gotCursor)
	}
}

func TestIngestRecordsFetchErrorCode(t *testing.T) {
	landing := &fakeLanding{}
	adapter := &fakeAdapter{err: &FetchError{Code: "http_status_503", Err: errors.New("503 Service Unavailable")}}

	_, err := newTestService(landing).Ingest(context.Background(), adapter, domain.Moscow)
	var fetchErr *FetchError
	if !errors.As(err, &fetchErr) || fetchErr.Code != "http_status_503" {
		t.Fatalf("err = %v", err)
	}
	if len(landing.saved) != 0 {
		t.Fatal("records saved after a failed fetch")
	}
	want := Run{City: domain.Moscow, AttemptAt: fixedNow, ErrorCode: "http_status_503"}
	if len(landing.runs) != 1 || !sameRun(landing.runs[0], want) {
		t.Fatalf("runs = %+v", landing.runs)
	}
}

func TestIngestRecordsStoreFailure(t *testing.T) {
	landing := &fakeLanding{saveErr: errors.New("connection reset")}
	adapter := &fakeAdapter{batch: Batch{Records: []domain.RawRecord{{ExternalID: "node/1"}}}}

	if _, err := newTestService(landing).Ingest(context.Background(), adapter, domain.Perm); err == nil {
		t.Fatal("expected an error")
	}
	if len(landing.runs) != 1 || landing.runs[0].ErrorCode != "internal" || landing.runs[0].Cursor != nil {
		t.Fatalf("runs = %+v", landing.runs)
	}
}

func sameRun(got, want Run) bool {
	return got.SourceID == want.SourceID && got.City == want.City && got.AttemptAt.Equal(want.AttemptAt) &&
		string(got.Cursor) == string(want.Cursor) && got.ErrorCode == want.ErrorCode
}
