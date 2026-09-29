package postgres

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/uber/h3-go/v4"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/materialize"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/normalize"
)

const placeResolution = 8
const fineResolution = 11

func durationSeconds(d time.Duration) int32 {
	seconds := int64(d / time.Second)
	if seconds < 0 {
		return 0
	}
	if seconds > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(seconds)
}

func interestMask(tags []string, bits map[string]int, owner string) (int64, error) {
	var mask int64
	for _, tag := range tags {
		bit, ok := bits[tag]
		if !ok {
			return 0, fmt.Errorf("%s: unknown interest tag %q", owner, tag)
		}
		mask |= 1 << bit
	}
	return mask, nil
}

func returnedIDs(rows pgx.Rows, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}
	ids, collectErr := pgx.CollectRows(rows, pgx.RowTo[string])
	if collectErr != nil {
		return nil, fmt.Errorf("collect returned IDs: %w", collectErr)
	}
	return ids, nil
}

// withdrawnSession is a session the source stopped listing, with what delivery needs to tell about it.
type withdrawnSession struct {
	SessionID, EventID, SourceRecordID uuid.UUID
	OldStatus                          string
	DataMode                           domain.DataMode
}

// writeEvent writes the event with its sessions and prices, retires the event rows the record had at other
// places and withdraws future sessions the source no longer lists. It returns the places whose projection
// the change affects.
//
//nolint:gocognit,funlen // event and session updates share one catalog transaction
func writeEvent(
	ctx context.Context,
	tx pgx.Tx,
	city domain.City,
	n *materialize.Normalized,
	placeID string,
	bits map[string]int,
	at time.Time,
) ([]string, []withdrawnSession, error) {
	e := n.Event
	mask, maskErr := interestMask(e.Tags, bits, "event "+e.ExternalID)
	if maskErr != nil {
		return nil, nil, maskErr
	}
	eventID := normalize.EntityID(string(n.Raw.Source) + ":" + e.ExternalID)
	record := n.Raw.SourceRecordID
	var touched []string
	touch := func(changed []string, place string) {
		if len(changed) > 0 {
			touched = append(touched, place)
		}
	}

	changed, err := returnedIDs(tx.Query(ctx, `
		INSERT INTO catalog.event AS t (id, city, place_id, title, normalized_title, category, tag_mask,
			organizer_name, age_min, data_mode, card_source_record_id, is_active, review_required, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7::bigint::bit(64), $8, $9, $10, $11, true, false, $12, $12)
		ON CONFLICT (id, city) DO UPDATE SET
			title = EXCLUDED.title, normalized_title = EXCLUDED.normalized_title, category = EXCLUDED.category,
			tag_mask = EXCLUDED.tag_mask, organizer_name = EXCLUDED.organizer_name, age_min = EXCLUDED.age_min,
			data_mode = EXCLUDED.data_mode, card_source_record_id = EXCLUDED.card_source_record_id,
			is_active = true, updated_at = EXCLUDED.updated_at
		WHERE (t.title, t.category, t.tag_mask, t.organizer_name, t.age_min, t.data_mode, t.card_source_record_id, t.is_active)
			IS DISTINCT FROM (EXCLUDED.title, EXCLUDED.category, EXCLUDED.tag_mask, EXCLUDED.organizer_name,
				EXCLUDED.age_min, EXCLUDED.data_mode, EXCLUDED.card_source_record_id, true)
		RETURNING id::text`,
		eventID, city, placeID, e.Title, e.NormalizedTitle, e.Category, mask, e.Organizer, e.AgeMin,
		n.Raw.DataMode, record, at))
	if err != nil {
		return nil, nil, fmt.Errorf("upsert event %s: %w", e.ExternalID, err)
	}
	touch(changed, placeID)

	moved, err := returnedIDs(tx.Query(ctx, `
		UPDATE catalog.event SET is_active = false, updated_at = $4
		WHERE city = $1 AND card_source_record_id = $2 AND id <> $3 AND is_active
		RETURNING place_id::text`, city, record, eventID, at))
	if err != nil {
		return nil, nil, fmt.Errorf("retire moved event %s: %w", e.ExternalID, err)
	}
	touched = append(touched, moved...)

	listed := make([]string, 0, len(e.Sessions))
	for i := range e.Sessions {
		s := &e.Sessions[i]
		sessionID := normalize.SessionID(eventID, s.StartsAt)
		listed = append(listed, sessionID.String())
		changed, sessionErr := returnedIDs(tx.Query(ctx, `
			INSERT INTO catalog.session AS t (id, city, event_id, slot_type, starts_at, ends_at, min_duration_s,
				recommended_duration_s, buffer_s, access_type, availability_status, is_hard_constraint, booking_url,
				data_mode, card_source_record_id, version, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 0, $9, 'unknown', $10, $11, $12, $13, 1, $14)
			ON CONFLICT (id, city) DO UPDATE SET
				slot_type = EXCLUDED.slot_type, ends_at = EXCLUDED.ends_at, min_duration_s = EXCLUDED.min_duration_s,
				recommended_duration_s = EXCLUDED.recommended_duration_s, access_type = EXCLUDED.access_type,
				availability_status = 'unknown', cancellation_reason = NULL,
				is_hard_constraint = EXCLUDED.is_hard_constraint, booking_url = EXCLUDED.booking_url,
				data_mode = EXCLUDED.data_mode, card_source_record_id = EXCLUDED.card_source_record_id,
				version = t.version + 1, updated_at = EXCLUDED.updated_at
			WHERE (t.slot_type, t.ends_at, t.min_duration_s, t.recommended_duration_s, t.access_type,
					t.availability_status, t.cancellation_reason, t.is_hard_constraint, t.booking_url, t.data_mode,
					t.card_source_record_id)
				IS DISTINCT FROM (EXCLUDED.slot_type, EXCLUDED.ends_at, EXCLUDED.min_duration_s,
					EXCLUDED.recommended_duration_s, EXCLUDED.access_type, 'unknown'::text, NULL::text,
					EXCLUDED.is_hard_constraint, EXCLUDED.booking_url, EXCLUDED.data_mode, EXCLUDED.card_source_record_id)
			RETURNING id::text`,
			sessionID, city, eventID, s.SlotType, s.StartsAt, s.EndsAt, durationSeconds(s.MinDuration),
			durationSeconds(s.RecommendedDuration), s.AccessType, s.SlotType == "FIXED_SESSION", s.BookingURL,
			n.Raw.DataMode, record, at))
		if sessionErr != nil {
			return nil, nil, fmt.Errorf(
				"upsert session %s of %s: %w",
				s.StartsAt.Format(time.RFC3339),
				e.ExternalID,
				sessionErr,
			)
		}
		touch(changed, placeID)

		var currency *string
		if s.Price.AmountMin != nil {
			currency = nullIfEmpty("RUB")
		}
		changed, err = returnedIDs(tx.Query(ctx, `
			INSERT INTO catalog.price_offer AS t (id, city, session_id, price_status, audience, tariff_label,
				amount_min, amount_max, currency, benefit_programs, purchase_url, source_record_id, observed_at,
				data_mode, is_active)
			VALUES ($1, $2, $3, $4, 'general', $5, $6, $7, $8, '{}', $9, $10, $11, $12, true)
			ON CONFLICT (id, city) DO UPDATE SET
				price_status = EXCLUDED.price_status, tariff_label = EXCLUDED.tariff_label,
				amount_min = EXCLUDED.amount_min, amount_max = EXCLUDED.amount_max, currency = EXCLUDED.currency,
				purchase_url = EXCLUDED.purchase_url, source_record_id = EXCLUDED.source_record_id,
				observed_at = EXCLUDED.observed_at, data_mode = EXCLUDED.data_mode, is_active = true
			WHERE (t.price_status, t.tariff_label, t.amount_min, t.amount_max, t.currency, t.purchase_url,
					t.source_record_id, t.data_mode, t.is_active)
				IS DISTINCT FROM (EXCLUDED.price_status, EXCLUDED.tariff_label, EXCLUDED.amount_min,
					EXCLUDED.amount_max, EXCLUDED.currency, EXCLUDED.purchase_url, EXCLUDED.source_record_id,
					EXCLUDED.data_mode, true)
			RETURNING id::text`,
			normalize.PriceID(sessionID), city, sessionID, s.Price.Status, s.Price.TariffLabel, s.Price.AmountMin,
			s.Price.AmountMax, currency, s.BookingURL, record, at, n.Raw.DataMode))
		if err != nil {
			return nil, nil, fmt.Errorf("upsert price of %s: %w", e.ExternalID, err)
		}
		touch(changed, placeID)
	}

	// A session missing from a version of an event that is still published was taken down by the source.
	recordID, err := uuid.Parse(record)
	if err != nil {
		return nil, nil, fmt.Errorf("source record id %q: %w", record, err)
	}
	rows, err := tx.Query(ctx, `
		WITH gone AS (
			SELECT id, event_id, availability_status AS old_status FROM catalog.session
			WHERE city = $1 AND card_source_record_id = $2 AND ends_at > $4
				AND availability_status <> 'cancelled' AND NOT (id = ANY ($3::uuid[]))
			FOR UPDATE)
		UPDATE catalog.session s SET availability_status = 'cancelled', cancellation_reason = 'source_removed',
			version = s.version + 1, updated_at = $4
		FROM gone WHERE s.city = $1 AND s.id = gone.id
		RETURNING s.id, gone.event_id, gone.old_status, s.data_mode`, city, record, listed, at)
	if err != nil {
		return nil, nil, fmt.Errorf("withdraw sessions of %s: %w", e.ExternalID, err)
	}
	withdrawn, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (withdrawnSession, error) {
		w := withdrawnSession{SourceRecordID: recordID}
		return w, row.Scan(&w.SessionID, &w.EventID, &w.OldStatus, &w.DataMode)
	})
	if err != nil {
		return nil, nil, fmt.Errorf("withdraw sessions of %s: %w", e.ExternalID, err)
	}
	if len(withdrawn) > 0 {
		ids := make([]string, len(withdrawn))
		for i := range withdrawn {
			ids[i] = withdrawn[i].SessionID.String()
		}
		if _, err := tx.Exec(ctx, `UPDATE catalog.price_offer SET is_active = false
			WHERE city = $1 AND session_id = ANY ($2::uuid[]) AND is_active`, city, ids); err != nil {
			return nil, nil, fmt.Errorf("retire prices of withdrawn sessions of %s: %w", e.ExternalID, err)
		}
		touched = append(touched, placeID)
	}
	return touched, withdrawn, nil
}

// projectPlaces rebuilds the search projection of the places, reading their coordinates from the catalog.
// A venue left with neither a category of its own nor an active event has nothing to be found by, so its
// projection is hidden.
func projectPlaces(ctx context.Context, tx pgx.Tx, city domain.City, ids []string, revision int64, at time.Time) error {
	for _, id := range ids {
		var lat, lon float64
		var searchable bool
		if err := tx.QueryRow(ctx, `
			SELECT ST_Y(p.coordinates), ST_X(p.coordinates), p.category IS NOT NULL OR EXISTS (
				SELECT 1 FROM catalog.event e WHERE e.place_id = p.id AND e.city = p.city AND e.is_active)
			FROM catalog.place p WHERE p.id = $1 AND p.city = $2`, id, city).Scan(&lat, &lon, &searchable); err != nil {
			return fmt.Errorf("read place %s: %w", id, err)
		}
		if !searchable {
			if _, err := tx.Exec(
				ctx,
				`UPDATE catalog.leisure_poi SET is_active = false, catalog_revision = $3, updated_at = $4
				WHERE id = $1 AND city = $2`,
				id,
				city,
				revision,
				at,
			); err != nil {
				return fmt.Errorf("hide place %s: %w", id, err)
			}
			continue
		}
		point := h3.NewLatLng(lat, lon)
		cell8, err := h3.LatLngToCell(point, placeResolution)
		if err != nil {
			return fmt.Errorf("place %s: h3 cell: %w", id, err)
		}
		cell11, err := h3.LatLngToCell(point, fineResolution)
		if err != nil {
			return fmt.Errorf("place %s: h3 cell: %w", id, err)
		}
		if err := projectPlace(ctx, tx, id, string(city), int64(cell8), int64(cell11), revision, at); err != nil {
			return fmt.Errorf("project place %s: %w", id, err)
		}
	}
	return nil
}
