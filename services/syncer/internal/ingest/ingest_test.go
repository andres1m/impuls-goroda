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
	sourceID       SourceID
	cursor         json.RawMessage
	unchanged      map[string]bool
	saveErr        error
	ensured        []domain.Source
	saved          []savedRecord
	runs           []Run
	unpublished    []Envelope
	unpublishedErr error
	advanced       []time.Time
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

func (l *fakeLanding) Unpublished(context.Context, SourceID, domain.City) ([]Envelope, error) {
	return l.unpublished, l.unpublishedErr
}

func (l *fakeLanding) AdvancePublished(_ context.Context, _ SourceID, _ domain.City, at time.Time) error {
	l.advanced = append(l.advanced, at)
	return nil
}

type fakePublisher struct {
	err       error
	published [][]Envelope
}

func (p *fakePublisher) Publish(_ context.Context, envelopes []Envelope) error {
	p.published = append(p.published, envelopes)
	return p.err
}

var fixedNow = time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)

func newTestService(l *fakeLanding, p *fakePublisher) *Service {
	return NewService(l, p, func() time.Time { return fixedNow })
}

func TestIngestSavesRecordsAndCursor(t *testing.T) {
	landing := &fakeLanding{sourceID: SourceID{1}, unchanged: map[string]bool{"event:2": true}}
	adapter := &fakeAdapter{batch: Batch{
		Records: []domain.RawRecord{{ExternalID: "event:1"}, {ExternalID: "event:2"}},
		Skipped: 3,
		Cursor:  json.RawMessage(`{"snapshot_at":"2026-09-27T10:00:00Z"}`),
	}}

	result, err := newTestService(landing, &fakePublisher{}).Ingest(context.Background(), adapter, domain.Perm)
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

	if _, err := newTestService(landing, &fakePublisher{}).Ingest(context.Background(), adapter, domain.Moscow); err != nil {
		t.Fatal(err)
	}
	if string(adapter.gotCursor) != `{"dataset_version":12}` {
		t.Fatalf("adapter got cursor %s", adapter.gotCursor)
	}
}

func TestIngestRecordsFetchErrorCode(t *testing.T) {
	landing := &fakeLanding{}
	adapter := &fakeAdapter{err: &FetchError{Code: "http_status_503", Err: errors.New("503 Service Unavailable")}}

	_, err := newTestService(landing, &fakePublisher{}).Ingest(context.Background(), adapter, domain.Moscow)
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

	if _, err := newTestService(landing, &fakePublisher{}).Ingest(context.Background(), adapter, domain.Perm); err == nil {
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

func TestIngestPublishesUnpublishedTail(t *testing.T) {
	older := validEnvelope()
	older.FetchedAt = fixedNow.Add(-time.Hour)
	fresh := validEnvelope()
	fresh.RawIngestID = "1c6d4f7e-3e2b-4d9f-8a4c-8b7e6f5d4c3b"
	landing := &fakeLanding{sourceID: SourceID{1}, unpublished: []Envelope{older, fresh}}
	publisher := &fakePublisher{}
	adapter := &fakeAdapter{batch: Batch{Records: []domain.RawRecord{{ExternalID: "event:1"}}, Cursor: json.RawMessage(`{}`)}}

	result, err := newTestService(landing, publisher).Ingest(context.Background(), adapter, domain.Moscow)
	if err != nil {
		t.Fatal(err)
	}
	if result.Published != 2 || len(publisher.published) != 1 || len(publisher.published[0]) != 2 {
		t.Fatalf("result = %+v, published = %+v", result, publisher.published)
	}
	if len(landing.advanced) != 1 || !landing.advanced[0].Equal(fixedNow) {
		t.Fatalf("advanced = %v", landing.advanced)
	}
	if len(landing.runs) != 1 || landing.runs[0].ErrorCode != "" {
		t.Fatalf("runs = %+v", landing.runs)
	}
}

func TestIngestEmptyTailAdvancesWithoutPublishing(t *testing.T) {
	landing := &fakeLanding{}
	publisher := &fakePublisher{}

	if _, err := newTestService(landing, publisher).Ingest(context.Background(), &fakeAdapter{}, domain.Perm); err != nil {
		t.Fatal(err)
	}
	if len(publisher.published) != 0 || len(landing.advanced) != 1 {
		t.Fatalf("published = %+v, advanced = %v", publisher.published, landing.advanced)
	}
}

func TestIngestPublishFailureKeepsWatermarkAndCursor(t *testing.T) {
	landing := &fakeLanding{unpublished: []Envelope{validEnvelope()}}
	publisher := &fakePublisher{err: errors.New("kafka: context deadline exceeded")}
	adapter := &fakeAdapter{batch: Batch{Cursor: json.RawMessage(`{"snapshot_at":"2026-09-27T10:00:00Z"}`)}}

	_, err := newTestService(landing, publisher).Ingest(context.Background(), adapter, domain.Moscow)
	if err == nil {
		t.Fatal("expected an error")
	}
	if len(landing.advanced) != 0 {
		t.Fatalf("watermark advanced after failed publish: %v", landing.advanced)
	}
	want := Run{City: domain.Moscow, AttemptAt: fixedNow, Cursor: adapter.batch.Cursor, ErrorCode: "publish"}
	if len(landing.runs) != 1 || !sameRun(landing.runs[0], want) {
		t.Fatalf("runs = %+v", landing.runs)
	}
}
