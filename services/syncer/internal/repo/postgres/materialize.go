package postgres

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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

func (s *MaterializeStore) PendingBatch(
	ctx context.Context,
	city domain.City,
	ids []string,
) ([]materialize.Raw, error) {
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
		scanErr := row.Scan(&r.ID, &r.SourceRecordID, &r.Source, &r.ExternalID, &r.Payload,
			&r.ContentHash, &r.AcceptedHash, &r.FetchedAt, &r.DataMode, &r.Latest)
		if scanErr != nil {
			return r, fmt.Errorf("scan pending raw ingest: %w", scanErr)
		}
		return r, nil
	})
	if err != nil {
		return nil, fmt.Errorf("read pending raw ingest: %w", err)
	}
	return raws, nil
}

// Publish writes a prepared batch in one transaction under the city lock. Only raw records still
// pending once the lock is held are written, so a retried publish repeats nothing.
//
//nolint:gocognit,cyclop,funlen // catalog writes and revision publication share one transaction
func (s *MaterializeStore) Publish(
	ctx context.Context,
	city domain.City,
	o *materialize.Outcome,
	at time.Time,
) (publishedRevision int64, published bool, publishErr error) {
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
	if lockErr := tx.QueryRow(ctx, `SELECT catalog_revision FROM ref.city WHERE code = $1 FOR UPDATE`, city).
		Scan(&revision); lockErr != nil {
		return 0, false, fmt.Errorf("lock city %s: %w", city, lockErr)
	}
	pending, err := stillPending(ctx, tx, o)
	o = &pending
	if err != nil {
		return 0, false, err
	}
	ref, err := LoadSeedReference(ctx, tx)
	if err != nil {
		return 0, false, err
	}

	touched := make(map[string]bool)
	var lifecycleChanges []lifecycleTransition
	placeIDs := make([]string, len(o.Apply))
	// Events sharing a place may each describe it a little differently; writing the place once, from the
	// batch's last description, keeps a repeated batch from counting it as changed.
	last := make(map[string]int, len(o.Apply))
	for i := range o.Apply {
		n := &o.Apply[i]
		placeIDs[i] = placeID(n)
		last[placeIDs[i]] = i
	}
	for i := range o.Apply {
		n := &o.Apply[i]
		if last[placeIDs[i]] != i {
			continue
		}
		updated, err := upsertPlace(ctx, tx, city, n, placeIDs[i], ref.TagBits, at)
		if err != nil {
			return 0, false, err
		}
		if updated {
			touched[placeIDs[i]] = true
		}
	}
	for i := range o.Apply {
		n := &o.Apply[i]
		if n.Event == nil {
			continue
		}
		places, changes, err := writeEvent(ctx, tx, city, n, placeIDs[i], ref.TagBits, at)
		if err != nil {
			return 0, false, err
		}
		lifecycleChanges = append(lifecycleChanges, changes...)
		for _, id := range places {
			touched[id] = true
		}
	}
	if len(touched) > 0 {
		if err := tx.QueryRow(ctx, `
			UPDATE ref.city SET catalog_revision = catalog_revision + 1, updated_at = $2
			WHERE code = $1 RETURNING catalog_revision`, city, at).Scan(&revision); err != nil {
			return 0, false, fmt.Errorf("bump catalog revision: %w", err)
		}
		if err := projectPlaces(ctx, tx, city, slices.Sorted(maps.Keys(touched)), revision, at); err != nil {
			return 0, false, err
		}
		announcement := catalogevent.Invalidation{
			City:            string(city),
			CatalogRevision: revision,
			Reason:          catalogevent.ReasonIngest,
			PublishedAt:     at,
		}
		if err := EnqueueRevision(ctx, tx, &announcement); err != nil {
			return 0, false, err
		}
		if err := enqueueLifecycle(ctx, tx, city, revision, at, lifecycleChanges); err != nil {
			return 0, false, err
		}
	}
	if err := settle(ctx, tx, city, o, at); err != nil {
		return 0, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, false, fmt.Errorf("commit: %w", err)
	}
	return revision, len(touched) > 0, nil
}

// stillPending locks the batch's raw records and drops those another publish already settled.
//
//nolint:gocognit // all outcome categories use the same locked pending set
func stillPending(ctx context.Context, tx pgx.Tx, o *materialize.Outcome) (materialize.Outcome, error) {
	var ids []string
	for i := range o.Apply {
		n := &o.Apply[i]
		ids = append(ids, n.Raw.ID)
	}
	combined := slices.Concat(o.Unchanged, o.Superseded)
	for i := range combined {
		ids = append(ids, combined[i].ID)
	}
	for i := range o.Failed {
		f := &o.Failed[i]
		ids = append(ids, f.Raw.ID)
	}
	for i := range o.Quarantined {
		ids = append(ids, o.Quarantined[i].Raw.ID)
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
	for i := range o.Apply {
		n := &o.Apply[i]
		if keep[n.Raw.ID] {
			out.Apply = append(out.Apply, *n)
		}
	}
	for i := range o.Unchanged {
		r := &o.Unchanged[i]
		if keep[r.ID] {
			out.Unchanged = append(out.Unchanged, *r)
		}
	}
	for i := range o.Superseded {
		r := &o.Superseded[i]
		if keep[r.ID] {
			out.Superseded = append(out.Superseded, *r)
		}
	}
	for i := range o.Failed {
		f := &o.Failed[i]
		if keep[f.Raw.ID] {
			out.Failed = append(out.Failed, *f)
		}
	}
	for i := range o.Quarantined {
		q := &o.Quarantined[i]
		if keep[q.Raw.ID] {
			out.Quarantined = append(out.Quarantined, *q)
		}
	}
	return out, nil
}

func placeID(n *materialize.Normalized) string {
	return normalize.EntityID(string(n.Raw.Source) + ":" + n.Place.ExternalID).String()
}

// upsertPlace writes the place and reports whether its row changed.
func upsertPlace(
	ctx context.Context,
	tx pgx.Tx,
	city domain.City,
	n *materialize.Normalized,
	id string,
	bits map[string]int,
	at time.Time,
) (updated bool, err error) {
	p := n.Place
	mask, err := interestMask(p.Tags, bits, "place "+p.ExternalID)
	if err != nil {
		return false, err
	}
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
		id, city, p.Title, p.NormalizedTitle, nullIfEmpty(p.Category), mask, p.Lon, p.Lat,
		p.Address, string(p.OpeningRules), n.Raw.DataMode, n.Raw.SourceRecordID, at)
	if err != nil {
		return false, fmt.Errorf("upsert place %s: %w", p.ExternalID, err)
	}
	returned, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return false, fmt.Errorf("upsert place %s: %w", p.ExternalID, err)
	}
	return len(returned) > 0, nil
}

// settle records what the batch did to its raw and source records and advances the watermark of
// every source in it no further than the earliest record still pending, failed or quarantined.
func settle(ctx context.Context, tx pgx.Tx, city domain.City, o *materialize.Outcome, at time.Time) error {
	ids := settledIDs(o)
	if len(ids.applied)+len(ids.failed)+len(ids.quarantined) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE integration.source_record sr SET accepted_hash = ri.content_hash
		FROM integration.raw_ingest ri
		WHERE ri.id = ANY ($1::uuid[]) AND sr.id = ri.source_record_id`, ids.accepted); err != nil {
		return fmt.Errorf("accept content hashes: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE integration.raw_ingest SET processing_state = CASE
			WHEN id = ANY ($2::uuid[]) THEN 'failed' WHEN id = ANY ($3::uuid[]) THEN 'quarantined' ELSE 'applied' END
		WHERE id = ANY ($1::uuid[]) OR id = ANY ($2::uuid[]) OR id = ANY ($3::uuid[])`,
		ids.applied, ids.failed, ids.quarantined); err != nil {
		return fmt.Errorf("mark raw ingest: %w", err)
	}
	if err := quarantine(ctx, tx, city, o.Quarantined, at); err != nil {
		return err
	}
	if err := resolveQuarantine(ctx, tx, ids.accepted, at); err != nil {
		return err
	}
	return advanceWatermark(ctx, tx, city, slices.Concat(ids.applied, ids.failed, ids.quarantined))
}

type settled struct {
	// accepted is the part of applied whose content the catalog now holds.
	accepted, applied, failed, quarantined []string
}

func settledIDs(o *materialize.Outcome) settled {
	var s settled
	for i := range o.Apply {
		s.accepted = append(s.accepted, o.Apply[i].Raw.ID)
	}
	for i := range o.Unchanged {
		s.accepted = append(s.accepted, o.Unchanged[i].ID)
	}
	s.applied = append(s.applied, s.accepted...)
	for i := range o.Superseded {
		s.applied = append(s.applied, o.Superseded[i].ID)
	}
	for i := range o.Failed {
		s.failed = append(s.failed, o.Failed[i].Raw.ID)
	}
	for i := range o.Quarantined {
		s.quarantined = append(s.quarantined, o.Quarantined[i].Raw.ID)
	}
	return s
}

func advanceWatermark(ctx context.Context, tx pgx.Tx, city domain.City, settledIDs []string) error {
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
						WHERE bs.source_id = s.source_id AND bs.city = $1
							AND b.processing_state IN ('pending', 'failed', 'quarantined')
					), 'infinity')
			) AS fetched_at
			FROM sources s
		)
		INSERT INTO integration.sync_cursor AS c (source_id, city, materialized_watermark)
		SELECT source_id, $1, jsonb_build_object('fetched_at', fetched_at) FROM marks WHERE fetched_at IS NOT NULL
		ON CONFLICT (source_id, city) DO UPDATE SET materialized_watermark = EXCLUDED.materialized_watermark
		WHERE c.materialized_watermark IS NULL
			OR (c.materialized_watermark->>'fetched_at')::timestamptz < (EXCLUDED.materialized_watermark->>'fetched_at')::timestamptz`,
		city, settledIDs)
	if err != nil {
		return fmt.Errorf("advance materialized watermark: %w", err)
	}
	return nil
}

// Reopen makes the latest raw record of every source record of the source and city pending again and
// forgets its accepted content, so the next batch materializes it against the current clock. Records
// already pending are returned too: a run that stopped before signalling them resumes.
func (s *MaterializeStore) Reopen(ctx context.Context, source domain.SourceKey, city domain.City) ([]string, error) {
	pool, err := s.connected()
	if err != nil {
		return nil, err
	}
	ids, err := returnedIDs(pool.Query(ctx, `
		WITH latest AS (
			SELECT DISTINCT ON (ri.source_record_id) ri.id, ri.source_record_id, ri.processing_state
			FROM integration.raw_ingest ri
			JOIN integration.source_record sr ON sr.id = ri.source_record_id
			JOIN integration.source src ON src.id = sr.source_id
			WHERE src.source_key = $1 AND sr.city = $2
			ORDER BY ri.source_record_id, ri.fetched_at DESC, ri.id DESC
		), reopened AS (
			UPDATE integration.raw_ingest ri SET processing_state = 'pending'
			FROM latest l WHERE ri.id = l.id AND l.processing_state = 'applied'
			RETURNING ri.source_record_id
		), forgotten AS (
			UPDATE integration.source_record sr SET accepted_hash = NULL
			FROM reopened r WHERE sr.id = r.source_record_id
		)
		SELECT id::text FROM latest WHERE processing_state IN ('applied', 'pending') ORDER BY id::text`, source, city))
	if err != nil {
		return nil, fmt.Errorf("reopen %s %s: %w", source, city, err)
	}
	return ids, nil
}
