package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

const UserAgent = "impuls-goroda-syncer"

type SourceID [16]byte

type Batch struct {
	Records []domain.RawRecord
	Skipped int
	Cursor  json.RawMessage
}

type Adapter interface {
	Source() domain.Source
	Fetch(ctx context.Context, city domain.City, cursor json.RawMessage) (Batch, error)
}

// Run is the outcome of one ingestion attempt. An empty ErrorCode means success.
// Cursor is stored whenever it is set: a run can fail after a successful fetch.
type Run struct {
	SourceID  SourceID
	City      domain.City
	AttemptAt time.Time
	Cursor    json.RawMessage
	ErrorCode string
}

type Landing interface {
	EnsureSource(ctx context.Context, source domain.Source) (SourceID, error)
	Cursor(ctx context.Context, source SourceID, city domain.City) (json.RawMessage, error)
	SaveRecord(ctx context.Context, source SourceID, city domain.City, mode domain.DataMode, record domain.RawRecord, fetchedAt time.Time) (inserted bool, err error)
	FinishRun(ctx context.Context, run Run) error
}

// FetchError carries a code that is safe to persist: it never contains URLs,
// query parameters or response bodies.
type FetchError struct {
	Code string
	Err  error
}

func (e *FetchError) Error() string { return e.Code + ": " + e.Err.Error() }
func (e *FetchError) Unwrap() error { return e.Err }

type Result struct {
	Received int
	Inserted int
	Skipped  int
}

type Service struct {
	landing Landing
	now     func() time.Time
}

func NewService(landing Landing, now func() time.Time) *Service {
	return &Service{landing: landing, now: now}
}

func (s *Service) Ingest(ctx context.Context, adapter Adapter, city domain.City) (Result, error) {
	source := adapter.Source()
	sourceID, err := s.landing.EnsureSource(ctx, source)
	if err != nil {
		return Result{}, fmt.Errorf("ensure source %s: %w", source.Key, err)
	}
	cursor, err := s.landing.Cursor(ctx, sourceID, city)
	if err != nil {
		return Result{}, fmt.Errorf("read cursor: %w", err)
	}

	attemptAt := s.now()
	batch, err := adapter.Fetch(ctx, city, cursor)
	if err != nil {
		return Result{}, s.fail(ctx, sourceID, city, attemptAt, fmt.Errorf("fetch %s: %w", source.Key, err))
	}

	result := Result{Received: len(batch.Records), Skipped: batch.Skipped}
	for _, record := range batch.Records {
		inserted, err := s.landing.SaveRecord(ctx, sourceID, city, source.DataMode, record, attemptAt)
		if err != nil {
			return result, s.fail(ctx, sourceID, city, attemptAt, fmt.Errorf("save %s: %w", record.ExternalID, err))
		}
		if inserted {
			result.Inserted++
		}
	}

	run := Run{SourceID: sourceID, City: city, AttemptAt: attemptAt, Cursor: batch.Cursor}
	if err := s.landing.FinishRun(ctx, run); err != nil {
		return result, fmt.Errorf("finish run: %w", err)
	}
	return result, nil
}

func (s *Service) fail(ctx context.Context, sourceID SourceID, city domain.City, attemptAt time.Time, cause error) error {
	run := Run{SourceID: sourceID, City: city, AttemptAt: attemptAt, ErrorCode: errorCode(cause)}
	if err := s.landing.FinishRun(ctx, run); err != nil {
		return errors.Join(cause, fmt.Errorf("record failed run: %w", err))
	}
	return cause
}

func errorCode(err error) string {
	var fetchErr *FetchError
	if errors.As(err, &fetchErr) {
		return fetchErr.Code
	}
	return "internal"
}
