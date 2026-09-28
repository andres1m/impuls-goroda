package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/uber/h3-go/v4"

	"github.com/andres1m/impuls-goroda/pkg/catalogevent"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/materialize"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/normalize"
)

// MaterializeStore reaches the pool through a function: the pool exists only once the database
// component has started.
type MaterializeStore struct {
	pool func() *pgxpool.Pool
}

func NewMaterializeStore(pool func() *pgxpool.Pool) *MaterializeStore {
	return &MaterializeStore{pool: pool}
}

var errNotConnected = errors.New("database is not connected")

func (s *MaterializeStore) connected() (*pgxpool.Pool, error) {
	if p := s.pool(); p != nil {
		return p, nil
	}
	return nil, errNotConnected
}

func (s *MaterializeStore) PendingBatch(ctx context.Context, city domain.City, ids []string) ([]materialize.Raw, error) {
	pool, err := s.connected()
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx, `
		SELECT ri.id::text, ri.source_record_id::text, src.source_key, sr.external_id, ri.raw_payload,
			ri.content_hash, sr.accepted_hash, ri.fetched_at, ri.data_mode,
			NOT EXISTS (SELECT 1 FROM integration.raw_ingest later
				WHERE later.source_record_id = ri.source_record_id
					AND (later.fetched_at, later.id) > (ri.fetched_at, ri.id))
		FROM integration.raw_ingest ri
		JOIN integration.source_record sr ON sr.id = ri.source_record_id
		JOIN integration.source src ON src.id = sr.source_id
		WHERE ri.id = ANY ($2::uuid[]) AND sr.city = $1 AND ri.processing_state = 'pending'
		ORDER BY ri.fetched_at, ri.id`, city, ids)
	if err != nil {
		return nil, fmt.Errorf("query pending raw ingest: %w", err)
	}
	raws, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (materialize.Raw, error) {
		var r materialize.Raw
		err := row.Scan(&r.ID, &r.SourceRecordID, &r.Source, &r.ExternalID, &r.Payload,
			&r.ContentHash, &r.AcceptedHash, &r.FetchedAt, &r.DataMode, &r.Latest)
		return r, err
	})
	if err != nil {
		return nil, fmt.Errorf("read pending raw ingest: %w", err)
	}
	return raws, nil
}

// Publish writes a prepared batch in one transaction under the city lock. Only raw records still
// pending once the lock is held are written, so a retried publish repeats nothing.
func (s *MaterializeStore) Publish(ctx context.Context, city domain.City, o materialize.Outcome, at time.Time) (int64, bool, error) {
	pool, err := s.connected()
	if err != nil {
		return 0, false, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // a no-op after commit

	var revision int64
	if err := tx.QueryRow(ctx, `SELECT catalog_revision FROM ref.city WHERE code = $1 FOR UPDATE`, city).Scan(&revision); err != nil {
		return 0, false, fmt.Errorf("lock city %s: %w", city, err)
	}
	o, err = stillPending(ctx, tx, o)
	if err != nil {
		return 0, false, err
	}
	ref, err := LoadSeedReference(ctx, tx)
	if err != nil {
		return 0, false, err
	}

	type changed struct {
		id          string
		res8, res11 int64
	}
	var places []changed
	for _, n := range o.Apply {
		id, res8, res11, updated, err := upsertPlace(ctx, tx, city, n, ref.TagBits, at)
		if err != nil {
			return 0, false, err
		}
		if updated {
			places = append(places, changed{id, res8, res11})
		}
	}
	if len(places) > 0 {
		if err := tx.QueryRow(ctx, `
			UPDATE ref.city SET catalog_revision = catalog_revision + 1, updated_at = $2
			WHERE code = $1 RETURNING catalog_revision`, city, at).Scan(&revision); err != nil {
			return 0, false, fmt.Errorf("bump catalog revision: %w", err)
		}
		for _, p := range places {
			if err := projectPlace(ctx, tx, p.id, string(city), p.res8, p.res11, revision, at); err != nil {
				return 0, false, fmt.Errorf("project place %s: %w", p.id, err)
			}
		}
		announcement := catalogevent.Invalidation{City: string(city), CatalogRevision: revision, Reason: catalogevent.ReasonIngest, PublishedAt: at}
		if err := EnqueueRevision(ctx, tx, announcement); err != nil {
			return 0, false, err
		}
	}
	if err := settle(ctx, tx, city, o); err != nil {
		return 0, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, false, fmt.Errorf("commit: %w", err)
	}
	return revision, len(places) > 0, nil
}

// stillPending locks the batch's raw records and drops those another publish already settled.
func stillPending(ctx context.Context, tx pgx.Tx, o materialize.Outcome) (materialize.Outcome, error) {
	var ids []string
	for _, n := range o.Apply {
		ids = append(ids, n.Raw.ID)
	}
	for _, r := range slices.Concat(o.Unchanged, o.Superseded) {
		ids = append(ids, r.ID)
	}
	for _, f := range o.Failed {
		ids = append(ids, f.Raw.ID)
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text FROM integration.raw_ingest
		WHERE id = ANY ($1::uuid[]) AND processing_state = 'pending'
		ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return materialize.Outcome{}, fmt.Errorf("lock raw ingest: %w", err)
	}
	pending, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return materialize.Outcome{}, fmt.Errorf("lock raw ingest: %w", err)
	}
	keep := make(map[string]bool, len(pending))
	for _, id := range pending {
		keep[id] = true
	}
	var out materialize.Outcome
	for _, n := range o.Apply {
		if keep[n.Raw.ID] {
			out.Apply = append(out.Apply, n)
		}
	}
	for _, r := range o.Unchanged {
		if keep[r.ID] {
			out.Unchanged = append(out.Unchanged, r)
		}
	}
	for _, r := range o.Superseded {
		if keep[r.ID] {
			out.Superseded = append(out.Superseded, r)
		}
	}
	for _, f := range o.Failed {
		if keep[f.Raw.ID] {
			out.Failed = append(out.Failed, f)
		}
	}
	return out, nil
}

// upsertPlace writes the place and reports whether its row changed.
func upsertPlace(ctx context.Context, tx pgx.Tx, city domain.City, n materialize.Normalized, bits map[string]int, at time.Time) (id string, res8, res11 int64, updated bool, err error) {
	p := n.Place
	var mask int64
	for _, tag := range p.Tags {
		bit, ok := bits[tag]
		if !ok {
			return "", 0, 0, false, fmt.Errorf("place %s: unknown interest tag %q", p.ExternalID, tag)
		}
		mask |= 1 << bit
	}
	point := h3.NewLatLng(p.Lat, p.Lon)
	cell8, err := h3.LatLngToCell(point, 8)
	if err != nil {
		return "", 0, 0, false, fmt.Errorf("place %s: h3 cell: %w", p.ExternalID, err)
	}
	cell11, err := h3.LatLngToCell(point, 11)
	if err != nil {
		return "", 0, 0, false, fmt.Errorf("place %s: h3 cell: %w", p.ExternalID, err)
	}
	id = normalize.EntityID(string(n.Raw.Source) + ":" + p.ExternalID).String()
	rows, err := tx.Query(ctx, `
		INSERT INTO catalog.place AS t (id, city, title, normalized_title, category, tag_mask, coordinates,
			address_text, opening_rules, data_mode, card_source_record_id, is_active, review_required, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6::bigint::bit(64), ST_SetSRID(ST_MakePoint($7, $8), 4326),
			$9, $10::jsonb, $11, $12, true, false, $13, $13)
		ON CONFLICT (id, city) DO UPDATE SET
			title = EXCLUDED.title, normalized_title = EXCLUDED.normalized_title, category = EXCLUDED.category,
			tag_mask = EXCLUDED.tag_mask, coordinates = EXCLUDED.coordinates, address_text = EXCLUDED.address_text,
			opening_rules = EXCLUDED.opening_rules, data_mode = EXCLUDED.data_mode,
			card_source_record_id = EXCLUDED.card_source_record_id, is_active = true, updated_at = EXCLUDED.updated_at
		WHERE (t.title, t.category, t.tag_mask, t.coordinates, t.address_text, t.opening_rules, t.data_mode,
				t.card_source_record_id, t.is_active)
			IS DISTINCT FROM (EXCLUDED.title, EXCLUDED.category, EXCLUDED.tag_mask, EXCLUDED.coordinates,
				EXCLUDED.address_text, EXCLUDED.opening_rules, EXCLUDED.data_mode, EXCLUDED.card_source_record_id, true)
		RETURNING id::text`,
		id, city, p.Title, p.NormalizedTitle, p.Category, mask, p.Lon, p.Lat,
		p.Address, string(p.OpeningRules), n.Raw.DataMode, n.Raw.SourceRecordID, at)
	if err != nil {
		return "", 0, 0, false, fmt.Errorf("upsert place %s: %w", p.ExternalID, err)
	}
	returned, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return "", 0, 0, false, fmt.Errorf("upsert place %s: %w", p.ExternalID, err)
	}
	return id, int64(cell8), int64(cell11), len(returned) > 0, nil
}

// settle records what the batch did to its raw and source records and advances the watermark of
// every source in it no further than the earliest record still pending or failed.
func settle(ctx context.Context, tx pgx.Tx, city domain.City, o materialize.Outcome) error {
	var accepted, applied, failed []string
	for _, n := range o.Apply {
		accepted = append(accepted, n.Raw.ID)
	}
	for _, r := range o.Unchanged {
		accepted = append(accepted, r.ID)
	}
	applied = append(applied, accepted...)
	for _, r := range o.Superseded {
		applied = append(applied, r.ID)
	}
	for _, f := range o.Failed {
		failed = append(failed, f.Raw.ID)
	}
	if len(applied)+len(failed) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE integration.source_record sr SET accepted_hash = ri.content_hash
		FROM integration.raw_ingest ri
		WHERE ri.id = ANY ($1::uuid[]) AND sr.id = ri.source_record_id`, accepted); err != nil {
		return fmt.Errorf("accept content hashes: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE integration.raw_ingest SET processing_state = CASE WHEN id = ANY ($2::uuid[]) THEN 'failed' ELSE 'applied' END
		WHERE id = ANY ($1::uuid[]) OR id = ANY ($2::uuid[])`, applied, failed); err != nil {
		return fmt.Errorf("mark raw ingest: %w", err)
	}
	_, err := tx.Exec(ctx, `
		WITH sources AS (
			SELECT DISTINCT sr.source_id FROM integration.raw_ingest ri
			JOIN integration.source_record sr ON sr.id = ri.source_record_id
			WHERE ri.id = ANY ($2::uuid[])
		), marks AS (
			SELECT s.source_id, (
				SELECT max(ri.fetched_at) FROM integration.raw_ingest ri
				JOIN integration.source_record sr ON sr.id = ri.source_record_id
				WHERE sr.source_id = s.source_id AND sr.city = $1 AND ri.processing_state = 'applied'
					AND ri.fetched_at < COALESCE((
						SELECT min(b.fetched_at) FROM integration.raw_ingest b
						JOIN integration.source_record bs ON bs.id = b.source_record_id
						WHERE bs.source_id = s.source_id AND bs.city = $1 AND b.processing_state IN ('pending', 'failed')
					), 'infinity')
			) AS fetched_at
			FROM sources s
		)
		INSERT INTO integration.sync_cursor AS c (source_id, city, materialized_watermark)
		SELECT source_id, $1, jsonb_build_object('fetched_at', fetched_at) FROM marks WHERE fetched_at IS NOT NULL
		ON CONFLICT (source_id, city) DO UPDATE SET materialized_watermark = EXCLUDED.materialized_watermark
		WHERE c.materialized_watermark IS NULL
			OR (c.materialized_watermark->>'fetched_at')::timestamptz < (EXCLUDED.materialized_watermark->>'fetched_at')::timestamptz`,
		city, slices.Concat(applied, failed))
	if err != nil {
		return fmt.Errorf("advance materialized watermark: %w", err)
	}
	return nil
}
