// Package materialize turns stored raw records into catalog rows, one city batch at a time.
package materialize

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/normalize"
)

var (
	records = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "syncer_materialize_records_total",
		Help: "Raw records handled by materialization, by outcome.",
	}, []string{"source", "result"})
	batches = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "syncer_materialize_batches_total",
		Help: "Materialized batches, by whether they published a catalog revision.",
	}, []string{"city", "published"})
)

// Raw is a pending raw record with what its source record already accepted.
type Raw struct {
	ID             string
	SourceRecordID string
	Source         domain.SourceKey
	ExternalID     string
	Payload        []byte
	ContentHash    []byte
	AcceptedHash   []byte
	FetchedAt      time.Time
	DataMode       domain.DataMode
	// False when a later raw record of the same source record exists; only the latest is applied.
	Latest bool
}

type Normalized struct {
	Raw   Raw
	Place normalize.PlaceDraft
	Event *normalize.EventDraft
}

type Rejected struct {
	Raw  Raw
	Code string
}

// Outcome is a prepared batch. Every raw record in it leaves the pending state when published.
type Outcome struct {
	Apply      []Normalized
	Unchanged  []Raw
	Superseded []Raw
	Failed     []Rejected
}

func (o *Outcome) empty() bool {
	return len(o.Apply)+len(o.Unchanged)+len(o.Superseded)+len(o.Failed) == 0
}

type Store interface {
	PendingBatch(ctx context.Context, city domain.City, ids []string) ([]Raw, error)
	// Publish writes the outcome under the city lock; published is false when no catalog row changed.
	Publish(ctx context.Context, city domain.City, o *Outcome, at time.Time) (revision int64, published bool, err error)
}

type Result struct {
	Applied, Unchanged, Superseded, Failed, Deferred int
	CatalogRevision                                  int64
	// Failures tell why each failed record could not become a catalog row.
	Failures []Failure
}

type Failure struct {
	RawIngestID string
	Source      domain.SourceKey
	Code        string
}

type normalizer func(city domain.City, externalID string, payload []byte, now time.Time) (normalize.Draft, error)

var normalizers = map[domain.SourceKey]normalizer{
	domain.MkrfEvents:      normalize.MkrfEvent,
	domain.KudaGo:          normalize.KudaGoEvent,
	domain.SyntheticSource: nil,
	domain.OSM: func(_ domain.City, externalID string, payload []byte, _ time.Time) (normalize.Draft, error) {
		place, err := normalize.OSMPlace(externalID, payload)
		return normalize.Draft{Place: place}, err
	},
}

// Prepare sorts a batch without touching the database; records of sources without a normalizer
// yet stay pending and are only counted.
func Prepare(city domain.City, raws []Raw, now time.Time) (o Outcome, deferred []Raw) {
	for i := range raws {
		r := &raws[i]
		normalizeRecord := normalizers[r.Source]
		switch {
		case !r.Latest:
			o.Superseded = append(o.Superseded, *r)
		case r.AcceptedHash != nil && bytes.Equal(r.ContentHash, r.AcceptedHash):
			o.Unchanged = append(o.Unchanged, *r)
		case normalizeRecord == nil:
			deferred = append(deferred, *r)
		default:
			draft, err := normalizeRecord(city, r.ExternalID, r.Payload, now)
			if bad, ok := errors.AsType[*normalize.DataError](err); ok {
				o.Failed = append(o.Failed, Rejected{*r, bad.Code})
				continue
			}
			o.Apply = append(o.Apply, Normalized{Raw: *r, Place: draft.Place, Event: draft.Event})
		}
	}
	return o, deferred
}

// Apply materializes the batch's raw records that are still pending. Repeating it is harmless:
// records it already handled are no longer pending.
func Apply(ctx context.Context, s Store, city domain.City, ids []string, now func() time.Time) (Result, error) {
	raws, err := s.PendingBatch(ctx, city, ids)
	if err != nil {
		return Result{}, fmt.Errorf("read pending batch: %w", err)
	}
	at := now().UTC()
	o, deferred := Prepare(city, raws, at)
	res := Result{Deferred: len(deferred)}
	count(&o, deferred)
	if o.empty() {
		return res, nil
	}
	revision, published, err := s.Publish(ctx, city, &o, at)
	if err != nil {
		return Result{}, fmt.Errorf("publish batch: %w", err)
	}
	batches.WithLabelValues(string(city), strconv.FormatBool(published)).Inc()
	res.Applied, res.Unchanged, res.Superseded, res.Failed = len(
		o.Apply,
	), len(
		o.Unchanged,
	), len(
		o.Superseded,
	), len(
		o.Failed,
	)
	res.CatalogRevision = revision
	for i := range o.Failed {
		f := &o.Failed[i]
		res.Failures = append(res.Failures, Failure{RawIngestID: f.Raw.ID, Source: f.Raw.Source, Code: f.Code})
	}
	return res, nil
}

func count(o *Outcome, deferred []Raw) {
	add := func(r *Raw, result string) { records.WithLabelValues(string(r.Source), result).Inc() }
	for i := range o.Apply {
		add(&o.Apply[i].Raw, "applied")
	}
	for i := range o.Unchanged {
		add(&o.Unchanged[i], "unchanged")
	}
	for i := range o.Superseded {
		add(&o.Superseded[i], "superseded")
	}
	for i := range o.Failed {
		add(&o.Failed[i].Raw, "failed")
	}
	for i := range deferred {
		add(&deferred[i], "deferred")
	}
}
