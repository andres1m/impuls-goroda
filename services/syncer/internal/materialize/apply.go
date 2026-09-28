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

func (o Outcome) empty() bool {
	return len(o.Apply)+len(o.Unchanged)+len(o.Superseded)+len(o.Failed) == 0
}

type Store interface {
	PendingBatch(ctx context.Context, city domain.City, ids []string) ([]Raw, error)
	// Publish writes the outcome under the city lock; published is false when no catalog row changed.
	Publish(ctx context.Context, city domain.City, o Outcome, at time.Time) (revision int64, published bool, err error)
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

var normalizers = map[domain.SourceKey]func(externalID string, payload []byte) (normalize.PlaceDraft, error){
	domain.OSM: normalize.OSMPlace,
}

// Prepare sorts a batch without touching the database; records of sources without a normalizer
// yet stay pending and are only counted.
func Prepare(raws []Raw) (o Outcome, deferred []Raw) {
	for _, r := range raws {
		normalizer, known := normalizers[r.Source]
		switch {
		case !r.Latest:
			o.Superseded = append(o.Superseded, r)
		case r.AcceptedHash != nil && bytes.Equal(r.ContentHash, r.AcceptedHash):
			o.Unchanged = append(o.Unchanged, r)
		case !known:
			deferred = append(deferred, r)
		default:
			place, err := normalizer(r.ExternalID, r.Payload)
			var bad *normalize.DataError
			if errors.As(err, &bad) {
				o.Failed = append(o.Failed, Rejected{r, bad.Code})
				continue
			}
			o.Apply = append(o.Apply, Normalized{r, place})
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
	o, deferred := Prepare(raws)
	res := Result{Deferred: len(deferred)}
	count(o, deferred)
	if o.empty() {
		return res, nil
	}
	revision, published, err := s.Publish(ctx, city, o, now().UTC())
	if err != nil {
		return Result{}, fmt.Errorf("publish batch: %w", err)
	}
	batches.WithLabelValues(string(city), strconv.FormatBool(published)).Inc()
	res.Applied, res.Unchanged, res.Superseded, res.Failed = len(o.Apply), len(o.Unchanged), len(o.Superseded), len(o.Failed)
	res.CatalogRevision = revision
	for _, f := range o.Failed {
		res.Failures = append(res.Failures, Failure{RawIngestID: f.Raw.ID, Source: f.Raw.Source, Code: f.Code})
	}
	return res, nil
}

func count(o Outcome, deferred []Raw) {
	add := func(r Raw, result string) { records.WithLabelValues(string(r.Source), result).Inc() }
	for _, n := range o.Apply {
		add(n.Raw, "applied")
	}
	for _, r := range o.Unchanged {
		add(r, "unchanged")
	}
	for _, r := range o.Superseded {
		add(r, "superseded")
	}
	for _, f := range o.Failed {
		add(f.Raw, "failed")
	}
	for _, r := range deferred {
		add(r, "deferred")
	}
}
