package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/materialize"
)

// OutsideBoundary answers for all points in one query; a city without a boundary answers NULL, which
// means the check cannot be made.
func (s *MaterializeStore) OutsideBoundary(
	ctx context.Context,
	city domain.City,
	points []materialize.Point,
) (outside []bool, known bool, err error) {
	pool, err := s.connected()
	if err != nil {
		return nil, false, err
	}
	lats, lons := make([]float64, len(points)), make([]float64, len(points))
	for i, p := range points {
		lats[i], lons[i] = p.Lat, p.Lon
	}
	rows, err := pool.Query(ctx, `
		SELECT NOT ST_Covers(c.boundary, ST_SetSRID(ST_MakePoint(p.lon, p.lat), 4326))
		FROM ref.city c, unnest($2::float8[], $3::float8[]) WITH ORDINALITY AS p (lat, lon, ord)
		WHERE c.code = $1
		ORDER BY p.ord`, city, lats, lons)
	if err != nil {
		return nil, false, fmt.Errorf("check boundary of %s: %w", city, err)
	}
	answers, err := pgx.CollectRows(rows, pgx.RowTo[*bool])
	if err != nil {
		return nil, false, fmt.Errorf("check boundary of %s: %w", city, err)
	}
	if len(answers) != len(points) {
		return nil, false, fmt.Errorf("check boundary of %s: %d answers for %d points", city, len(answers), len(points))
	}
	outside = make([]bool, len(points))
	for i, a := range answers {
		if a == nil {
			return nil, false, nil
		}
		outside[i] = *a
	}
	return outside, true, nil
}

// quarantine sets the batch's malformed records aside with their reasons. A malformed payload also has
// to reach the dead letter topic, which the outbox guarantees once this transaction commits.
func quarantine(ctx context.Context, tx pgx.Tx, city domain.City, qs []materialize.Quarantined, at time.Time) error {
	if len(qs) == 0 {
		return nil
	}
	ids, reasons, details := make([]string, len(qs)), make([]string, len(qs)), make([]string, len(qs))
	for i := range qs {
		q := &qs[i]
		d, err := json.Marshal(q.Details)
		if err != nil {
			return fmt.Errorf("encode quarantine details: %w", err)
		}
		ids[i], reasons[i], details[i] = q.Raw.ID, string(q.Reason), string(d)
		if q.Reason == domain.InvalidSchema {
			if err := EnqueueDeadLetter(ctx, tx, city, &q.Raw, q.Details.Code, at); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO integration.quarantine (id, raw_ingest_id, reason_code, details, state, created_at)
		SELECT gen_random_uuid(), q.raw_ingest_id, q.reason_code, q.details::jsonb, 'open', $4
		FROM unnest($1::uuid[], $2::text[], $3::text[]) AS q (raw_ingest_id, reason_code, details)
		ON CONFLICT (raw_ingest_id, reason_code) DO NOTHING`, ids, reasons, details, at); err != nil {
		return fmt.Errorf("quarantine raw ingest: %w", err)
	}
	return nil
}

// resolveQuarantine closes the open quarantine of a source record's earlier versions once a version of
// it is accepted.
func resolveQuarantine(ctx context.Context, tx pgx.Tx, accepted []string, at time.Time) error {
	if len(accepted) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE integration.quarantine q SET state = 'resolved', resolved_at = $2
		FROM integration.raw_ingest ri
		WHERE q.raw_ingest_id = ri.id AND q.state = 'open'
			AND ri.source_record_id IN (SELECT source_record_id FROM integration.raw_ingest WHERE id = ANY ($1::uuid[]))`,
		accepted, at); err != nil {
		return fmt.Errorf("resolve quarantine: %w", err)
	}
	return nil
}
