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
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/normalize"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/resolve"
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
	quarantines = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "syncer_quarantine_total",
		Help: "Raw records set aside in quarantine, by source and reason.",
	}, []string{"source", "reason"})
	geoChecks = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "syncer_geo_check_total",
		Help: "Places checked against their city boundary, by result.",
	}, []string{"city", "result"})
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
	Raw        Raw
	Place      normalize.PlaceDraft
	Event      *normalize.EventDraft
	Resolution resolve.Resolution
}

type Rejected struct {
	Raw  Raw
	Code string
}

// QuarantineDetails is kept with a quarantined record to tell what exactly was wrong with it.
type QuarantineDetails struct {
	Code  string  `json:"code,omitempty"`
	Check string  `json:"check,omitempty"`
	Lat   float64 `json:"lat,omitempty"`
	Lon   float64 `json:"lon,omitempty"`
}

type Quarantined struct {
	Raw     Raw
	Reason  domain.QuarantineReason
	Details QuarantineDetails
}

type Point struct{ Lat, Lon float64 }

// Outcome is a prepared batch. Every raw record in it leaves the pending state when published.
type Outcome struct {
	Apply       []Normalized
	Unchanged   []Raw
	Superseded  []Raw
	Failed      []Rejected
	Quarantined []Quarantined
}

func (o *Outcome) empty() bool {
	return len(o.Apply)+len(o.Unchanged)+len(o.Superseded)+len(o.Failed)+len(o.Quarantined) == 0
}

type Store interface {
	PendingBatch(ctx context.Context, city domain.City, ids []string) ([]Raw, error)
	// Publish writes the outcome under the city lock; published is false when no catalog row changed.
	Publish(ctx context.Context, city domain.City, o *Outcome, at time.Time) (revision int64, published bool, err error)
	// OutsideBoundary tells for each point whether it lies outside the city; known is false while the
	// city has no boundary.
	OutsideBoundary(ctx context.Context, city domain.City, points []Point) (outside []bool, known bool, err error)
	// Resolve settles, for each place of the batch, whether it is one the catalog already has.
	Resolve(ctx context.Context, city domain.City, o *Outcome) error
}

type Result struct {
	Applied, Unchanged, Superseded, Failed, Quarantined, Deferred int
	CatalogRevision                                               int64
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

// Only these data errors mean the record itself is malformed; the others are well-formed records the
// catalog cannot use.
var quarantineReasons = map[string]domain.QuarantineReason{
	"bad_payload":     domain.InvalidSchema,
	"bad_coordinates": domain.CorruptedGeometry,
}

func reject(o *Outcome, r *Raw, code string) {
	if reason, malformed := quarantineReasons[code]; malformed {
		o.Quarantined = append(
			o.Quarantined,
			Quarantined{Raw: *r, Reason: reason, Details: QuarantineDetails{Code: code}},
		)
		return
	}
	o.Failed = append(o.Failed, Rejected{*r, code})
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
				reject(&o, r, bad.Code)
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
	if boundaryErr := isolateOutsiders(ctx, s, city, &o); boundaryErr != nil {
		return Result{}, boundaryErr
	}
	if resolveErr := s.Resolve(ctx, city, &o); resolveErr != nil {
		return Result{}, fmt.Errorf("resolve places: %w", resolveErr)
	}
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
	res.Applied = len(o.Apply)
	res.Unchanged = len(o.Unchanged)
	res.Superseded = len(o.Superseded)
	res.Failed = len(o.Failed)
	res.Quarantined = len(o.Quarantined)
	res.CatalogRevision = revision
	for i := range o.Failed {
		f := &o.Failed[i]
		res.Failures = append(res.Failures, Failure{RawIngestID: f.Raw.ID, Source: f.Raw.Source, Code: f.Code})
	}
	for i := range o.Quarantined {
		q := &o.Quarantined[i]
		res.Failures = append(
			res.Failures,
			Failure{RawIngestID: q.Raw.ID, Source: q.Raw.Source, Code: string(q.Reason)},
		)
	}
	return res, nil
}

// isolateOutsiders moves records whose place lies outside the city into quarantine.
func isolateOutsiders(ctx context.Context, s Store, city domain.City, o *Outcome) error {
	if len(o.Apply) == 0 {
		return nil
	}
	points := make([]Point, len(o.Apply))
	for i := range o.Apply {
		points[i] = Point{Lat: o.Apply[i].Place.Lat, Lon: o.Apply[i].Place.Lon}
	}
	outside, known, err := s.OutsideBoundary(ctx, city, points)
	if err != nil {
		return fmt.Errorf("check city boundary: %w", err)
	}
	if !known {
		geoChecks.WithLabelValues(string(city), "no_boundary").Add(float64(len(points)))
		return nil
	}
	if len(outside) != len(points) {
		return fmt.Errorf("city boundary answered %d of %d points", len(outside), len(points))
	}
	kept := o.Apply[:0]
	for i := range o.Apply {
		n := &o.Apply[i]
		if !outside[i] {
			geoChecks.WithLabelValues(string(city), "inside").Inc()
			kept = append(kept, *n)
			continue
		}
		geoChecks.WithLabelValues(string(city), "outside").Inc()
		o.Quarantined = append(o.Quarantined, Quarantined{Raw: n.Raw, Reason: domain.GeoDiscrepancy,
			Details: QuarantineDetails{Check: "outside_city_boundary", Lat: n.Place.Lat, Lon: n.Place.Lon}})
	}
	o.Apply = kept
	return nil
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
	for i := range o.Quarantined {
		q := &o.Quarantined[i]
		add(&q.Raw, "quarantined")
		quarantines.WithLabelValues(string(q.Raw.Source), string(q.Reason)).Inc()
		if q.Reason == domain.InvalidSchema {
			ingest.SchemaMismatch.WithLabelValues(string(q.Raw.Source)).Inc()
		}
	}
	for i := range deferred {
		add(&deferred[i], "deferred")
	}
}
