package postgres

import (
	"context"
	"crypto/rand"
	"slices"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/solver"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/usecase"
)

func randomBytes16[T ~[16]byte](t *testing.T) T {
	t.Helper()
	var id T
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	return id
}

type staticTransitProvider struct {
	transit solver.Transit
}

func (p staticTransitProvider) Transit(context.Context, string, []domain.Coordinate, []domain.MovementMode) (solver.Transit, bool, error) {
	return p.transit, false, nil
}

func TestCatalogCandidatesAndPlannerIntegration(t *testing.T) {
	ctx, tx := fixtureTx(t)

	if _, err := tx.Exec(ctx, `TRUNCATE catalog.price_offer, catalog.session, catalog.event, catalog.place_entrance, catalog.leisure_poi, catalog.place CASCADE`); err != nil {
		t.Fatal(err)
	}

	loc, err := time.LoadLocation("Asia/Yekaterinburg")
	if err != nil {
		t.Fatal(err)
	}
	cityUpdated := time.Date(2026, 9, 28, 7, 0, 0, 0, time.UTC)
	if _, err := tx.Exec(ctx, `UPDATE ref.city SET catalog_revision = 11, updated_at = $1 WHERE code = 'perm'`, cityUpdated); err != nil {
		t.Fatal(err)
	}

	sourceID := randomBytes16[[16]byte](t)
	recordID := randomBytes16[domain.SourceRecordID](t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO integration.source (id, source_key, name, access_mode, schema_version, is_enabled)
		VALUES ($1, 'test_fixture_src', 'Test Fixture Source', 'synthetic', '1', true)`, sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO integration.source_record (id, source_id, city, external_id, source_url, last_seen_at, data_mode)
		VALUES ($1, $2, 'perm', 'rec-1', 'https://example.org/rec-1', $3, 'synthetic')`, recordID, sourceID, cityUpdated); err != nil {
		t.Fatal(err)
	}

	// Place 1: Park with opening_rules (continuous visit without event) and an entrance.
	parkID := randomBytes16[domain.PlaceID](t)
	entranceID := randomBytes16[domain.EntranceID](t)
	parkRules := `{
		"schema_version": 1,
		"weekly": {
			"mon": [["09:00", "21:00"]],
			"tue": [["09:00", "21:00"]],
			"wed": [["09:00", "21:00"]],
			"thu": [["09:00", "21:00"]],
			"fri": [["09:00", "21:00"]],
			"sat": [["09:00", "21:00"]],
			"sun": [["09:00", "21:00"]]
		}
	}`
	parkMask := int64(domain.Interests(domain.InterestRunningPark, domain.InterestCityWalk))
	if _, err := tx.Exec(ctx, `
		INSERT INTO catalog.place (id, city, title, normalized_title, category, tag_mask, coordinates, opening_rules, data_mode, card_source_record_id, is_active, review_required, created_at, updated_at)
		VALUES ($1, 'perm', 'Esplanade Park', 'esplanade park', 'walk', $2::bigint::bit(64), ST_SetSRID(ST_MakePoint(56.245, 58.008), 4326), $3::jsonb, 'prepared', $4, true, false, $5, $5)`,
		parkID, parkMask, parkRules, recordID, cityUpdated); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO catalog.leisure_poi (id, city, title, normalized_title, categories, tag_mask, data_mode, coordinates, h3_res8, h3_res11, base_score, catalog_revision, is_active, updated_at)
		SELECT id, city, title, normalized_title, ARRAY['walk'], tag_mask, data_mode, coordinates, 0, 0, 10.0, 11, true, updated_at
		FROM catalog.place WHERE id = $1 AND city = 'perm'`, parkID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO catalog.place_entrance (id, city, place_id, coordinates, allowed_modes, accessibility_status, verification_status, updated_at)
		VALUES ($1, 'perm', $2, ST_SetSRID(ST_MakePoint(56.2451, 58.0081), 4326), ARRAY['walk', 'transit'], 'confirmed', 'verified', $3)`,
		entranceID, parkID, cityUpdated); err != nil {
		t.Fatal(err)
	}

	// Place 2: Theatre without place category (venue only for events), with two sessions:
	// - Session 1: available inside [10:00, 18:00] local time, with a fixed price and a currency-less unknown offer.
	// - Session 2: obligated session whose time moved outside [10:00, 18:00] local time (to 20:00-21:30).
	venueID := randomBytes16[domain.PlaceID](t)
	eventID := randomBytes16[domain.EventID](t)
	sessionInWindowID := randomBytes16[domain.SessionID](t)
	sessionMovedID := randomBytes16[domain.SessionID](t)
	offerFixedID := randomBytes16[domain.PriceOfferID](t)
	offerUnknownID := randomBytes16[domain.PriceOfferID](t)

	if _, err := tx.Exec(ctx, `
		INSERT INTO catalog.place (id, city, title, normalized_title, category, tag_mask, coordinates, opening_rules, data_mode, card_source_record_id, is_active, review_required, created_at, updated_at)
		VALUES ($1, 'perm', 'Opera House', 'opera house', NULL, 0::bit(64), ST_SetSRID(ST_MakePoint(56.248, 58.012), 4326), '{}'::jsonb, 'live', $2, true, false, $3, $3)`,
		venueID, recordID, cityUpdated); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO catalog.leisure_poi (id, city, title, normalized_title, categories, tag_mask, data_mode, coordinates, h3_res8, h3_res11, base_score, catalog_revision, is_active, updated_at)
		SELECT id, city, title, normalized_title, ARRAY['culture'], tag_mask, data_mode, coordinates, 0, 0, 10.0, 11, true, updated_at
		FROM catalog.place WHERE id = $1 AND city = 'perm'`, venueID); err != nil {
		t.Fatal(err)
	}

	eventMask := int64(domain.Interests(domain.InterestPerformingArts, domain.InterestClassicalArt))
	if _, err := tx.Exec(ctx, `
		INSERT INTO catalog.event (id, city, place_id, title, normalized_title, category, tag_mask, age_min, age_max, data_mode, card_source_record_id, is_active, review_required, created_at, updated_at)
		VALUES ($1, 'perm', $2, 'Ballet Matinee', 'ballet matinee', 'culture', $3::bigint::bit(64), 6, 99, 'synthetic', $4, true, false, $5, $5)`,
		eventID, venueID, eventMask, recordID, cityUpdated); err != nil {
		t.Fatal(err)
	}

	s1Start := time.Date(2026, 9, 28, 13, 0, 0, 0, loc).UTC()
	s1End := time.Date(2026, 9, 28, 14, 30, 0, 0, loc).UTC()
	if _, err := tx.Exec(ctx, `
		INSERT INTO catalog.session (id, city, event_id, slot_type, starts_at, ends_at, min_duration_s, recommended_duration_s, buffer_s, access_type, availability_status, availability_observed_at, is_hard_constraint, booking_url, data_mode, card_source_record_id, version, updated_at)
		VALUES ($1, 'perm', $2, 'FIXED_SESSION', $3, $4, 5400, 5400, 600, 'ticket', 'available', $5, true, 'https://example.org/book/1', 'synthetic', $6, 2, $5)`,
		sessionInWindowID, eventID, s1Start, s1End, cityUpdated, recordID); err != nil {
		t.Fatal(err)
	}

	s2Start := time.Date(2026, 9, 28, 20, 0, 0, 0, loc).UTC()
	s2End := time.Date(2026, 9, 28, 21, 30, 0, 0, loc).UTC()
	if _, err := tx.Exec(ctx, `
		INSERT INTO catalog.session (id, city, event_id, slot_type, starts_at, ends_at, min_duration_s, recommended_duration_s, buffer_s, access_type, availability_status, availability_observed_at, is_hard_constraint, data_mode, card_source_record_id, version, updated_at)
		VALUES ($1, 'perm', $2, 'FIXED_SESSION', $3, $4, 5400, 5400, 0, 'ticket', 'available', $5, true, 'synthetic', $6, 3, $5)`,
		sessionMovedID, eventID, s2Start, s2End, cityUpdated, recordID); err != nil {
		t.Fatal(err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO catalog.price_offer (id, city, session_id, price_status, audience, eligibility_age_min, eligibility_age_max, amount_min, amount_max, currency, benefit_programs, purchase_url, source_record_id, observed_at, data_mode, is_active)
		VALUES
			($1, 'perm', $3, 'fixed', 'general', 0, 99, 50000, 50000, 'RUB', ARRAY['pushkin_card'], 'https://example.org/buy/1', $4, $5, 'synthetic', true),
			($2, 'perm', $3, 'unknown', 'student', NULL, NULL, NULL, NULL, NULL, '{}', NULL, $4, $5, 'synthetic', true)`,
		offerFixedID, offerUnknownID, sessionInWindowID, recordID, cityUpdated); err != nil {
		t.Fatal(err)
	}

	// Switch to impuls_optimizer role inside the transaction to prove no integration.* access is required.
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE impuls_optimizer`); err != nil {
		t.Fatal(err)
	}

	catalog := NewCatalog(tx)
	req := validOptimizeRequest()
	req.Constraints.Obligations = []domain.Obligation{{
		SessionID:     &sessionMovedID,
		StartsAt:      &s1Start,
		Participation: domain.ParticipationUserReported,
	}}

	candidates, freshness, err := catalog.Candidates(ctx, req)
	if err != nil {
		t.Fatalf("Candidates under impuls_optimizer role: %v", err)
	}
	if err := freshness.Validate(); err != nil {
		t.Fatalf("freshness validate: %v", err)
	}
	if freshness.CatalogRevision != 11 || freshness.DataMode != domain.DataSynthetic {
		t.Fatalf("unexpected freshness: %+v", freshness)
	}
	if len(candidates) != 3 {
		t.Fatalf("expected 3 candidates (1 park window + 1 in-window session + 1 obligated moved session), got %d", len(candidates))
	}
	for i, c := range candidates {
		if err := c.Validate(); err != nil {
			t.Fatalf("candidate %d validate: %v", i, err)
		}
	}

	// Verify park candidate has its entrance and opening window.
	parkIdx := slices.IndexFunc(candidates, func(c domain.Candidate) bool { return c.Place.ID == parkID })
	if parkIdx < 0 || len(candidates[parkIdx].Entrances) != 1 || candidates[parkIdx].Entrances[0].ID != entranceID {
		t.Fatalf("park candidate missing entrance: %+v", candidates)
	}

	// Verify in-window session candidate has both offers (including defaulted RUB currency on unknown offer).
	sessIdx := slices.IndexFunc(candidates, func(c domain.Candidate) bool { return c.Session != nil && c.Session.ID == sessionInWindowID })
	if sessIdx < 0 || len(candidates[sessIdx].Offers) != 2 {
		t.Fatalf("session candidate missing offers: %+v", candidates)
	}

	// Run Planner.Optimize without the moved obligation and confirm a READY route is produced.
	req.Constraints.Obligations = nil
	bt, err := solver.NewBaselineTransit(solver.DefaultTransitParams())
	if err != nil {
		t.Fatal(err)
	}
	planner, err := usecase.NewPlanner(
		usecase.Config{Currency: "RUB", BeamWidth: 5, Parallelism: 2},
		catalog,
		staticTransitProvider{transit: bt},
		zap.NewNop(),
	)
	if err != nil {
		t.Fatal(err)
	}
	res, err := planner.Optimize(ctx, req)
	if err != nil {
		t.Fatalf("Planner.Optimize: %v", err)
	}
	if err := res.Validate(); err != nil {
		t.Fatalf("OptimizeResult.Validate: %v", err)
	}
	if res.Status != domain.ResultReady || len(res.Routes) == 0 {
		t.Fatalf("expected READY routes, got status=%s routes=%d", res.Status, len(res.Routes))
	}
}
