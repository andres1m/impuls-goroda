package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/materialize"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/normalize"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/resolve"
)

// createPlace inserts the place a record describes when the catalog does not have it yet; an existing
// place is only rewritten from its selected facts.
func createPlace(
	ctx context.Context,
	tx pgx.Tx,
	city domain.City,
	n *materialize.Normalized,
	id string,
	bits map[string]int,
	at time.Time,
) (bool, error) {
	p := n.Place
	mask, err := interestMask(p.Tags, bits, "place "+p.ExternalID)
	if err != nil {
		return false, err
	}
	rows, err := tx.Query(ctx, `
		INSERT INTO catalog.place (id, city, title, normalized_title, category, tag_mask, coordinates,
			address_text, opening_rules, data_mode, card_source_record_id, is_active, review_required, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6::bigint::bit(64) | COALESCE((SELECT en.llm_tag_mask FROM catalog.entity_enrichment en
				WHERE en.place_id = $1 AND en.city = $2), 0::bit(64)), ST_SetSRID(ST_MakePoint($7, $8), 4326),
			$9, $10::jsonb, $11, $12, true, $13, $14, $14)
		ON CONFLICT (id, city) DO NOTHING
		RETURNING id::text`,
		id, city, p.Title, p.NormalizedTitle, nullIfEmpty(p.Category), mask, p.Lon, p.Lat,
		p.Address, string(p.OpeningRules), n.Raw.DataMode, n.Raw.SourceRecordID,
		n.Resolution.Kind == resolve.Review, at)
	if err != nil {
		return false, fmt.Errorf("create place %s: %w", p.ExternalID, err)
	}
	created, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return false, fmt.Errorf("create place %s: %w", p.ExternalID, err)
	}
	return len(created) > 0, nil
}

type factValue struct {
	attribute string
	value     any
}

// placeFacts lists what a record says about its place; a value the record does not have is left out.
func placeFacts(p *normalize.PlaceDraft) []factValue {
	out := []factValue{
		{resolve.AttrSourcePlaceID, p.ExternalID},
		{resolve.AttrTitle, map[string]string{"title": p.Title, "normalized_title": p.NormalizedTitle}},
		{resolve.AttrCoordinates, map[string]float64{"lat": p.Lat, "lon": p.Lon}},
	}
	if p.Address != nil && *p.Address != "" {
		out = append(out, factValue{resolve.AttrAddress, *p.Address})
	}
	if p.Category != "" {
		out = append(out, factValue{resolve.AttrCategory, p.Category})
	}
	if len(p.Tags) > 0 {
		out = append(out, factValue{resolve.AttrTags, slices.Sorted(slices.Values(p.Tags))})
	}
	if rules := bytes.TrimSpace(p.OpeningRules); len(rules) > 0 && string(rules) != "{}" && string(rules) != "null" {
		out = append(out, factValue{resolve.AttrOpeningRules, json.RawMessage(rules)})
	}
	return out
}

// recordPlace links the record to the place and stores what it says. A record already linked keeps its
// link; a fact is added only when it differs from the record's latest one.
func recordPlace(
	ctx context.Context,
	tx pgx.Tx,
	city domain.City,
	n *materialize.Normalized,
	placeID string,
	trust resolve.Trust,
	at time.Time,
) error {
	status, score := "linked", any(nil)
	switch {
	case n.Resolution.Kind == resolve.Review:
		status = "review_required"
	case n.Resolution.Kind == resolve.Merge && !n.Resolution.Known:
		score = n.Resolution.Score
	}
	var linkID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO integration.entity_link (id, source_record_id, city, place_id, match_score, resolution_status, resolved_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (source_record_id, place_id, city) WHERE place_id IS NOT NULL
		DO UPDATE SET resolved_at = integration.entity_link.resolved_at
		RETURNING id::text`,
		uuid.New().String(), n.Raw.SourceRecordID, city, placeID, score, status, at).Scan(&linkID); err != nil {
		return fmt.Errorf("link %s: %w", n.Place.ExternalID, err)
	}
	for _, f := range placeFacts(&n.Place) {
		value, err := json.Marshal(f.value)
		if err != nil {
			return fmt.Errorf("encode %s of %s: %w", f.attribute, n.Place.ExternalID, err)
		}
		// The same raw record read again (after a normalization change) says its new value in the place of
		// the old one: two facts of one fetch could not be told apart by time.
		replaced, err := tx.Exec(ctx, `
			UPDATE integration.attribute_fact SET value = $4::jsonb
			WHERE entity_link_id = $1 AND attribute_name = $2 AND raw_ingest_id = $3 AND value IS DISTINCT FROM $4::jsonb`,
			linkID, f.attribute, n.Raw.ID, string(value))
		if err != nil {
			return fmt.Errorf("replace %s of %s: %w", f.attribute, n.Place.ExternalID, err)
		}
		if replaced.RowsAffected() > 0 {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO integration.attribute_fact (id, entity_link_id, raw_ingest_id, source_record_id, target_kind, target_id, city,
				attribute_name, value, fetched_at, data_mode, trust_rank, is_selected)
			SELECT $1, $2, $3, $4, 'place', $5, $6, $7, $8::jsonb, $9, $10, $11, false
			WHERE NOT EXISTS (SELECT 1 FROM integration.attribute_fact
					WHERE entity_link_id = $2 AND attribute_name = $7 AND raw_ingest_id = $3)
				AND (SELECT value FROM integration.attribute_fact
					WHERE entity_link_id = $2 AND attribute_name = $7 ORDER BY fetched_at DESC, id LIMIT 1)
					IS DISTINCT FROM $8::jsonb`,
			uuid.New().String(), linkID, n.Raw.ID, n.Raw.SourceRecordID, placeID, city, f.attribute, string(value),
			n.Raw.FetchedAt, n.Raw.DataMode, trust.Rank(f.attribute, n.Raw.Source)); err != nil {
			return fmt.Errorf("store %s of %s: %w", f.attribute, n.Place.ExternalID, err)
		}
	}
	return nil
}

// applyFacts makes the place take each attribute from its most trusted fact and reports whether the row
// changed.
func applyFacts(
	ctx context.Context,
	tx pgx.Tx,
	city domain.City,
	placeID string,
	trust resolve.Trust,
	bits map[string]int,
	at time.Time,
) (bool, error) {
	facts, err := latestFacts(ctx, tx, city, placeID)
	if err != nil {
		return false, err
	}
	var incumbent *string
	if err := tx.QueryRow(ctx, `SELECT card_source_record_id::text FROM catalog.place WHERE id = $1 AND city = $2`,
		placeID, city).Scan(&incumbent); err != nil {
		return false, fmt.Errorf("read card record of %s: %w", placeID, err)
	}
	current := ""
	if incumbent != nil {
		current = *incumbent
	}
	picked := trust.Select(facts, current)
	title, hasTitle := picked[resolve.AttrTitle]
	coords, hasCoords := picked[resolve.AttrCoordinates]
	if !hasTitle || !hasCoords {
		return false, fmt.Errorf("place %s has no title or coordinates among its facts", placeID)
	}
	for attr, f := range picked {
		if _, err := tx.Exec(ctx, `UPDATE integration.attribute_fact SET is_selected = false
			WHERE target_kind = 'place' AND target_id = $1 AND city = $2 AND attribute_name = $3 AND is_selected AND id <> $4`,
			placeID, city, attr, f.ID); err != nil {
			return false, fmt.Errorf("unselect %s of %s: %w", attr, placeID, err)
		}
		if _, err := tx.Exec(ctx, `UPDATE integration.attribute_fact SET is_selected = true WHERE id = $1 AND NOT is_selected`,
			f.ID); err != nil {
			return false, fmt.Errorf("select %s of %s: %w", attr, placeID, err)
		}
	}

	var name struct {
		Title           string `json:"title"`
		NormalizedTitle string `json:"normalized_title"`
	}
	var point struct {
		Lat float64 `json:"lat"`
		Lon float64 `json:"lon"`
	}
	if err := json.Unmarshal(title.Value, &name); err != nil {
		return false, fmt.Errorf("decode title fact of %s: %w", placeID, err)
	}
	if err := json.Unmarshal(coords.Value, &point); err != nil {
		return false, fmt.Errorf("decode coordinates fact of %s: %w", placeID, err)
	}
	address, err := optionalString(picked, resolve.AttrAddress)
	if err != nil {
		return false, err
	}
	category, err := optionalString(picked, resolve.AttrCategory)
	if err != nil {
		return false, err
	}
	var tags []string
	if f, ok := picked[resolve.AttrTags]; ok {
		if err := json.Unmarshal(f.Value, &tags); err != nil {
			return false, fmt.Errorf("decode tags fact of %s: %w", placeID, err)
		}
	}
	mask, err := interestMask(tags, bits, "place "+placeID)
	if err != nil {
		return false, err
	}
	rules := json.RawMessage(`{}`)
	if f, ok := picked[resolve.AttrOpeningRules]; ok {
		rules = f.Value
	}
	modes := make([]domain.DataMode, 0, len(picked))
	for _, f := range picked {
		modes = append(modes, f.DataMode)
	}
	rows, err := tx.Query(ctx, updatePlaceFromFactsSQL, placeID, city, name.Title, name.NormalizedTitle, category, mask,
		point.Lon, point.Lat, address, string(rules), string(resolve.LeastTrusted(modes...)), title.SourceRecordID, at)
	if err != nil {
		return false, fmt.Errorf("update place %s: %w", placeID, err)
	}
	changed, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return false, fmt.Errorf("update place %s: %w", placeID, err)
	}
	return len(changed) > 0, nil
}

func optionalString(picked map[string]resolve.Fact, attribute string) (*string, error) {
	f, ok := picked[attribute]
	if !ok {
		return nil, nil
	}
	var v string
	if err := json.Unmarshal(f.Value, &v); err != nil {
		return nil, fmt.Errorf("decode %s fact: %w", attribute, err)
	}
	return &v, nil
}

const updatePlaceFromFactsSQL = `
WITH v AS (SELECT $3::text AS title, $4::text AS normalized_title, $5::text AS category,
		($6::bigint::bit(64) | COALESCE((SELECT en.llm_tag_mask FROM catalog.entity_enrichment en
			WHERE en.place_id = $1 AND en.city = $2), 0::bit(64))) AS tag_mask,
		ST_SetSRID(ST_MakePoint($7, $8), 4326) AS coordinates, $9::text AS address, $10::jsonb AS rules,
		$11::text AS mode, $12::uuid AS card)
UPDATE catalog.place t SET title = v.title, normalized_title = v.normalized_title, category = v.category,
	tag_mask = v.tag_mask, coordinates = v.coordinates, address_text = v.address, opening_rules = v.rules,
	data_mode = v.mode, card_source_record_id = v.card, is_active = true, updated_at = $13
FROM v
WHERE t.id = $1 AND t.city = $2
	AND (t.title, t.category, t.tag_mask, t.coordinates, t.address_text, t.opening_rules, t.data_mode,
		t.card_source_record_id, t.is_active)
	IS DISTINCT FROM (v.title, v.category, v.tag_mask, v.coordinates, v.address, v.rules, v.mode, v.card, true)
RETURNING t.id::text`

func latestFacts(ctx context.Context, tx pgx.Tx, city domain.City, placeID string) ([]resolve.Fact, error) {
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT ON (f.entity_link_id, f.attribute_name)
			f.id::text, f.attribute_name, s.source_key, f.value, f.fetched_at, f.data_mode, f.source_record_id::text
		FROM integration.attribute_fact f
		JOIN integration.entity_link l ON l.id = f.entity_link_id
		JOIN integration.source_record r ON r.id = f.source_record_id
		JOIN integration.source s ON s.id = r.source_id
		WHERE l.place_id = $1 AND l.city = $2
		ORDER BY f.entity_link_id, f.attribute_name, f.fetched_at DESC, f.id`, placeID, city)
	if err != nil {
		return nil, fmt.Errorf("read facts of %s: %w", placeID, err)
	}
	facts, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (resolve.Fact, error) {
		var f resolve.Fact
		var source, mode string
		err := row.Scan(&f.ID, &f.Attribute, &source, &f.Value, &f.FetchedAt, &mode, &f.SourceRecordID)
		f.Source, f.DataMode = domain.SourceKey(source), domain.DataMode(mode)
		return f, err
	})
	if err != nil {
		return nil, fmt.Errorf("read facts of %s: %w", placeID, err)
	}
	return facts, nil
}
