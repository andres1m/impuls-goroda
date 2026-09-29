package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/andres1m/impuls-goroda/pkg/catalogevent"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/seed"
)

type SeedResult struct {
	Places, Events, Sessions, Prices int
	CatalogRevision                  int64
}

func LoadSeedReference(ctx context.Context, tx pgx.Tx) (seed.Reference, error) {
	rows, err := tx.Query(ctx, `SELECT code FROM ref.category WHERE is_active`)
	if err != nil {
		return seed.Reference{}, fmt.Errorf("read categories: %w", err)
	}
	categories, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return seed.Reference{}, fmt.Errorf("read categories: %w", err)
	}
	type tag struct {
		Code string
		Bit  int16
	}
	rows, err = tx.Query(ctx, `SELECT code, bit FROM ref.interest_tag`)
	if err != nil {
		return seed.Reference{}, fmt.Errorf("read interest tags: %w", err)
	}
	tags, err := pgx.CollectRows(rows, pgx.RowToStructByPos[tag])
	if err != nil {
		return seed.Reference{}, fmt.Errorf("read interest tags: %w", err)
	}
	ref := seed.Reference{Categories: make(map[string]bool), TagBits: make(map[string]int)}
	for _, code := range categories {
		ref.Categories[code] = true
	}
	for _, t := range tags {
		ref.TagBits[t.Code] = int(t.Bit)
	}
	return ref, nil
}

// ApplySeed writes one city's expanded synthetic dataset inside tx and publishes a new catalog revision.
// The city row stays locked until tx ends, as for any catalog publication.
func ApplySeed(ctx context.Context, tx pgx.Tx, rows *seed.Rows, at time.Time) (SeedResult, error) {
	var city string
	if err := tx.QueryRow(ctx, `SELECT code FROM ref.city WHERE code = $1 FOR UPDATE`, rows.City).
		Scan(&city); err != nil {
		return SeedResult{}, fmt.Errorf("lock city %s: %w", rows.City, err)
	}
	src := seed.Source(rows.Version)
	sourceID, err := upsertSource(ctx, tx, &src)
	if err != nil {
		return SeedResult{}, err
	}
	w := &seedWriter{tx: tx, city: rows.City, sourceID: uuidParam(sourceID), version: rows.Version, at: at,
		sessionRecords: make(map[uuid.UUID]pgtype.UUID, len(rows.Sessions))}

	if writeErr := w.writeAll(ctx, rows); writeErr != nil {
		return SeedResult{}, writeErr
	}
	hidden, err := w.deactivateMissing(ctx, rows)
	if err != nil {
		return SeedResult{}, err
	}

	var revision int64
	err = tx.QueryRow(ctx, `
		UPDATE ref.city SET catalog_revision = catalog_revision + 1, updated_at = $2
		WHERE code = $1 RETURNING catalog_revision`, rows.City, at).Scan(&revision)
	if err != nil {
		return SeedResult{}, fmt.Errorf("bump catalog revision: %w", err)
	}
	for i := range rows.Places {
		if projErr := w.project(ctx, &rows.Places[i], revision); projErr != nil {
			return SeedResult{}, projErr
		}
	}
	_, err = tx.Exec(ctx, `
		UPDATE catalog.leisure_poi SET is_active = false, catalog_revision = $3, updated_at = $4
		WHERE city = $1 AND id = ANY ($2::uuid[])`, rows.City, hidden, revision, at)
	if err != nil {
		return SeedResult{}, fmt.Errorf("hide removed places: %w", err)
	}
	announcement := catalogevent.Invalidation{
		City:            rows.City,
		CatalogRevision: revision,
		Reason:          catalogevent.ReasonSeed,
		PublishedAt:     at,
	}
	if enqErr := EnqueueRevision(ctx, tx, &announcement); enqErr != nil {
		return SeedResult{}, enqErr
	}
	return SeedResult{
		Places:          len(rows.Places),
		Events:          len(rows.Events),
		Sessions:        len(rows.Sessions),
		Prices:          len(rows.Prices),
		CatalogRevision: revision,
	}, nil
}

type seedWriter struct {
	tx             pgx.Tx
	city           string
	sourceID       pgtype.UUID
	version        string
	at             time.Time
	sessionRecords map[uuid.UUID]pgtype.UUID
}

func (w *seedWriter) writeAll(ctx context.Context, rows *seed.Rows) error {
	for i := range rows.Places {
		if err := w.place(ctx, &rows.Places[i]); err != nil {
			return err
		}
	}
	for i := range rows.Events {
		if err := w.event(ctx, &rows.Events[i]); err != nil {
			return err
		}
	}
	for i := range rows.Sessions {
		if err := w.session(ctx, &rows.Sessions[i]); err != nil {
			return err
		}
	}
	for i := range rows.Prices {
		if err := w.price(ctx, &rows.Prices[i]); err != nil {
			return err
		}
	}
	return nil
}

func (w *seedWriter) record(ctx context.Context, externalID string) (pgtype.UUID, error) {
	var id pgtype.UUID
	err := w.tx.QueryRow(ctx, `
		INSERT INTO integration.source_record
			(id, source_id, city, external_id, source_url, last_seen_at, provider_version, data_mode)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, 'synthetic')
		ON CONFLICT (source_id, external_id) DO UPDATE SET
			last_seen_at = EXCLUDED.last_seen_at,
			provider_version = EXCLUDED.provider_version
		RETURNING id`,
		w.sourceID, w.city, externalID, "synthetic:"+externalID, w.at, w.version,
	).Scan(&id)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("upsert source record %s: %w", externalID, err)
	}
	return id, nil
}

func (w *seedWriter) place(ctx context.Context, p *seed.PlaceRow) error {
	record, err := w.record(ctx, p.ExternalID)
	if err != nil {
		return err
	}
	_, err = w.tx.Exec(ctx, `
		INSERT INTO catalog.place AS t (id, city, title, normalized_title, category, tag_mask, coordinates,
			address_text, opening_rules, data_mode, card_source_record_id, is_active, review_required, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6::bigint::bit(64), ST_SetSRID(ST_MakePoint($7, $8), 4326),
			$9, $10::jsonb, 'synthetic', $11, true, false, $12, $12)
		ON CONFLICT (id, city) DO UPDATE SET
			title = EXCLUDED.title, normalized_title = EXCLUDED.normalized_title, category = EXCLUDED.category,
			tag_mask = EXCLUDED.tag_mask, coordinates = EXCLUDED.coordinates, address_text = EXCLUDED.address_text,
			opening_rules = EXCLUDED.opening_rules, card_source_record_id = EXCLUDED.card_source_record_id,
			is_active = true, updated_at = EXCLUDED.updated_at
		WHERE (t.title, t.category, t.tag_mask, t.coordinates, t.address_text, t.opening_rules,
				t.card_source_record_id, t.is_active)
			IS DISTINCT FROM (EXCLUDED.title, EXCLUDED.category, EXCLUDED.tag_mask, EXCLUDED.coordinates,
				EXCLUDED.address_text, EXCLUDED.opening_rules, EXCLUDED.card_source_record_id, true)`,
		p.ID.String(), w.city, p.Title, p.NormalizedTitle, p.Category, p.TagMask, p.Lon, p.Lat,
		p.Address, string(p.OpeningRules), record, w.at)
	if err != nil {
		return fmt.Errorf("upsert place %s: %w", p.ExternalID, err)
	}
	return nil
}

func (w *seedWriter) event(ctx context.Context, e *seed.EventRow) error {
	record, err := w.record(ctx, e.ExternalID)
	if err != nil {
		return err
	}
	_, err = w.tx.Exec(ctx, `
		INSERT INTO catalog.event AS t (id, city, place_id, title, normalized_title, category, tag_mask,
			organizer_name, age_min, data_mode, card_source_record_id, is_active, review_required, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7::bigint::bit(64), $8, $9, 'synthetic', $10, true, false, $11, $11)
		ON CONFLICT (id, city) DO UPDATE SET
			place_id = EXCLUDED.place_id, title = EXCLUDED.title, normalized_title = EXCLUDED.normalized_title,
			category = EXCLUDED.category, tag_mask = EXCLUDED.tag_mask, organizer_name = EXCLUDED.organizer_name,
			age_min = EXCLUDED.age_min, card_source_record_id = EXCLUDED.card_source_record_id,
			is_active = true, updated_at = EXCLUDED.updated_at
		WHERE (t.place_id, t.title, t.category, t.tag_mask, t.organizer_name, t.age_min,
				t.card_source_record_id, t.is_active)
			IS DISTINCT FROM (EXCLUDED.place_id, EXCLUDED.title, EXCLUDED.category, EXCLUDED.tag_mask,
				EXCLUDED.organizer_name, EXCLUDED.age_min, EXCLUDED.card_source_record_id, true)`,
		e.ID.String(), w.city, e.PlaceID.String(), e.Title, e.NormalizedTitle, e.Category, e.TagMask,
		e.Organizer, e.AgeMin, record, w.at)
	if err != nil {
		return fmt.Errorf("upsert event %s: %w", e.ExternalID, err)
	}
	return nil
}

func (w *seedWriter) session(ctx context.Context, s *seed.SessionRow) error {
	record, err := w.record(ctx, s.ExternalID)
	if err != nil {
		return err
	}
	w.sessionRecords[s.ID] = record
	_, err = w.tx.Exec(ctx, `
		INSERT INTO catalog.session AS t (id, city, event_id, slot_type, starts_at, ends_at, min_duration_s,
			recommended_duration_s, buffer_s, last_entry_at, late_entry_allowed, registration_deadline,
			access_type, availability_status, availability_observed_at, is_hard_constraint, cancellation_reason,
			data_mode, card_source_record_id, version, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17,
			'synthetic', $18, 1, $15)
		ON CONFLICT (id, city) DO UPDATE SET
			slot_type = EXCLUDED.slot_type, starts_at = EXCLUDED.starts_at, ends_at = EXCLUDED.ends_at,
			min_duration_s = EXCLUDED.min_duration_s, recommended_duration_s = EXCLUDED.recommended_duration_s,
			buffer_s = EXCLUDED.buffer_s, last_entry_at = EXCLUDED.last_entry_at,
			late_entry_allowed = EXCLUDED.late_entry_allowed, registration_deadline = EXCLUDED.registration_deadline,
			access_type = EXCLUDED.access_type, availability_status = EXCLUDED.availability_status,
			availability_observed_at = EXCLUDED.availability_observed_at,
			is_hard_constraint = EXCLUDED.is_hard_constraint, cancellation_reason = EXCLUDED.cancellation_reason,
			card_source_record_id = EXCLUDED.card_source_record_id,
			version = t.version + 1, updated_at = EXCLUDED.updated_at
		WHERE (t.slot_type, t.starts_at, t.ends_at, t.min_duration_s, t.recommended_duration_s, t.buffer_s,
				t.last_entry_at, t.late_entry_allowed, t.registration_deadline, t.access_type,
				t.availability_status, t.is_hard_constraint, t.cancellation_reason, t.card_source_record_id)
			IS DISTINCT FROM (EXCLUDED.slot_type, EXCLUDED.starts_at, EXCLUDED.ends_at, EXCLUDED.min_duration_s,
				EXCLUDED.recommended_duration_s, EXCLUDED.buffer_s, EXCLUDED.last_entry_at,
				EXCLUDED.late_entry_allowed, EXCLUDED.registration_deadline, EXCLUDED.access_type,
				EXCLUDED.availability_status, EXCLUDED.is_hard_constraint, EXCLUDED.cancellation_reason,
				EXCLUDED.card_source_record_id)`,
		s.ID.String(), w.city, s.EventID.String(), s.SlotType, s.StartsAt, s.EndsAt, s.MinDurationS,
		s.RecommendedDurationS, s.BufferS, s.LastEntryAt, s.LateEntryAllowed, s.RegistrationDeadline,
		s.AccessType, s.Availability, w.at, s.IsHard, s.CancellationReason, record)
	if err != nil {
		return fmt.Errorf("upsert session %s: %w", s.ExternalID, err)
	}
	return nil
}

func (w *seedWriter) price(ctx context.Context, p *seed.PriceRow) error {
	_, err := w.tx.Exec(ctx, `
		INSERT INTO catalog.price_offer AS t (id, city, session_id, price_status, audience, tariff_label,
			eligibility_age_min, eligibility_age_max, amount_min, amount_max, currency, benefit_programs,
			source_record_id, observed_at, data_mode, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, 'synthetic', true)
		ON CONFLICT (id, city) DO UPDATE SET
			price_status = EXCLUDED.price_status, tariff_label = EXCLUDED.tariff_label,
			eligibility_age_min = EXCLUDED.eligibility_age_min, eligibility_age_max = EXCLUDED.eligibility_age_max,
			amount_min = EXCLUDED.amount_min, amount_max = EXCLUDED.amount_max, currency = EXCLUDED.currency,
			benefit_programs = EXCLUDED.benefit_programs, source_record_id = EXCLUDED.source_record_id,
			observed_at = EXCLUDED.observed_at, is_active = true
		WHERE (t.price_status, t.tariff_label, t.eligibility_age_min, t.eligibility_age_max, t.amount_min,
				t.amount_max, t.currency, t.benefit_programs, t.source_record_id, t.is_active)
			IS DISTINCT FROM (EXCLUDED.price_status, EXCLUDED.tariff_label, EXCLUDED.eligibility_age_min,
				EXCLUDED.eligibility_age_max, EXCLUDED.amount_min, EXCLUDED.amount_max, EXCLUDED.currency,
				EXCLUDED.benefit_programs, EXCLUDED.source_record_id, true)`,
		p.ID.String(), w.city, p.SessionID.String(), p.Status, p.Audience, p.TariffLabel,
		p.EligibilityAgeMin, p.EligibilityAgeMax, p.AmountMin, p.AmountMax, p.Currency, p.BenefitPrograms,
		w.sessionRecords[p.SessionID], w.at)
	if err != nil {
		return fmt.Errorf("upsert price %s: %w", p.ID, err)
	}
	return nil
}

// deactivateMissing hides this source's places and events that the dataset no longer lists and returns
// the hidden place ids. Sessions stay untouched: saved routes may still reference them.
func (w *seedWriter) deactivateMissing(ctx context.Context, rows *seed.Rows) ([]string, error) {
	events := make([]string, len(rows.Events))
	for i := range rows.Events {
		events[i] = rows.Events[i].ID.String()
	}
	_, err := w.tx.Exec(ctx, `
		UPDATE catalog.event e SET is_active = false, updated_at = $4
		FROM integration.source_record r
		WHERE e.city = $1 AND e.card_source_record_id = r.id AND r.source_id = $2
			AND e.is_active AND NOT (e.id = ANY ($3::uuid[]))`,
		w.city, w.sourceID, events, w.at)
	if err != nil {
		return nil, fmt.Errorf("deactivate removed events: %w", err)
	}

	places := make([]string, len(rows.Places))
	for i := range rows.Places {
		places[i] = rows.Places[i].ID.String()
	}
	hidden, err := w.tx.Query(ctx, `
		UPDATE catalog.place p SET is_active = false, updated_at = $4
		FROM integration.source_record r
		WHERE p.city = $1 AND p.card_source_record_id = r.id AND r.source_id = $2
			AND p.is_active AND NOT (p.id = ANY ($3::uuid[]))
		RETURNING p.id::text`,
		w.city, w.sourceID, places, w.at)
	if err != nil {
		return nil, fmt.Errorf("deactivate removed places: %w", err)
	}
	ids, err := pgx.CollectRows(hidden, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("deactivate removed places: %w", err)
	}
	return ids, nil
}

func (w *seedWriter) project(ctx context.Context, p *seed.PlaceRow, revision int64) error {
	if err := projectPlace(ctx, w.tx, p.ID.String(), w.city, p.H3Res8, p.H3Res11, revision, w.at); err != nil {
		return fmt.Errorf("project place %s: %w", p.ExternalID, err)
	}
	return nil
}

// projectPlace rebuilds the place's search projection from the place and its active events.
func projectPlace(
	ctx context.Context,
	tx pgx.Tx,
	id, city string,
	h3Res8, h3Res11, revision int64,
	at time.Time,
) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO catalog.leisure_poi AS t (id, city, title, normalized_title, categories, tag_mask, data_mode,
			coordinates, h3_res8, h3_res11, base_score, benefit_programs, catalog_revision, is_active, updated_at)
		SELECT p.id, p.city, p.title, p.normalized_title,
			ARRAY(SELECT c FROM (
					SELECT p.category
					UNION
					SELECT e.category FROM catalog.event e
					WHERE e.place_id = p.id AND e.city = p.city AND e.is_active
				) AS categories (c)
				WHERE c IS NOT NULL ORDER BY c),
			p.tag_mask | COALESCE((SELECT bit_or(e.tag_mask) FROM catalog.event e
				WHERE e.place_id = p.id AND e.city = p.city AND e.is_active), 0::bit(64)),
			p.data_mode, p.coordinates, $3, $4, 1.0,
			ARRAY(SELECT DISTINCT program FROM catalog.event e
				JOIN catalog.session s ON s.event_id = e.id AND s.city = e.city
				JOIN catalog.price_offer o ON o.session_id = s.id AND o.city = s.city
				CROSS JOIN LATERAL unnest(o.benefit_programs) AS program
				WHERE e.place_id = p.id AND e.city = p.city AND e.is_active AND o.is_active
				ORDER BY program),
			$5, p.is_active, $6
		FROM catalog.place p
		WHERE p.id = $1 AND p.city = $2
		ON CONFLICT (id, city) DO UPDATE SET
			title = EXCLUDED.title, normalized_title = EXCLUDED.normalized_title,
			categories = EXCLUDED.categories, tag_mask = EXCLUDED.tag_mask, data_mode = EXCLUDED.data_mode,
			coordinates = EXCLUDED.coordinates, h3_res8 = EXCLUDED.h3_res8, h3_res11 = EXCLUDED.h3_res11,
			base_score = EXCLUDED.base_score, benefit_programs = EXCLUDED.benefit_programs,
			catalog_revision = EXCLUDED.catalog_revision, is_active = EXCLUDED.is_active,
			updated_at = EXCLUDED.updated_at`,
		id, city, h3Res8, h3Res11, revision, at)
	if err != nil {
		return fmt.Errorf("exec project place %s: %w", id, err)
	}
	return nil
}
