package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/usecase"
)

const defaultCurrency = "RUB"

// Catalog reads places, entrances, events, sessions and price offers from the city catalog.
type Catalog struct {
	db Querier
}

func NewCatalog(db Querier) *Catalog {
	return &Catalog{db: db}
}

const cityCatalogSQL = `
	SELECT timezone, catalog_revision, updated_at
	FROM ref.city
	WHERE code = $1`

const activePlacesSQL = `
	SELECT p.id, p.title, p.category, p.tag_mask::bigint,
		ST_X(p.coordinates), ST_Y(p.coordinates),
		p.opening_rules, p.data_mode, p.card_source_record_id, p.updated_at,
		COALESCE(lp.base_score::float8, 1.0)
	FROM catalog.place p
	LEFT JOIN catalog.leisure_poi lp ON lp.id = p.id AND lp.city = p.city
	WHERE p.city = $1 AND p.is_active
	ORDER BY p.id`

const placeEntrancesSQL = `
	SELECT e.id, e.place_id, ST_X(e.coordinates), ST_Y(e.coordinates),
		e.allowed_modes, e.accessibility_status, e.verification_status
	FROM catalog.place_entrance e
	JOIN catalog.place p ON p.id = e.place_id AND p.city = e.city
	WHERE e.city = $1 AND p.is_active
	ORDER BY e.place_id, e.id`

const activeSessionsSQL = `
	SELECT s.id, s.event_id, s.slot_type, s.starts_at, s.ends_at,
		s.min_duration_s, s.recommended_duration_s, s.buffer_s,
		s.last_entry_at, s.late_entry_allowed, s.registration_deadline,
		s.access_type, s.availability_status, s.availability_observed_at,
		s.is_hard_constraint, s.booking_url, s.data_mode, s.card_source_record_id,
		s.version, s.updated_at,
		e.place_id, e.title, e.category, e.tag_mask::bigint,
		e.age_min, e.age_max, e.data_mode, e.card_source_record_id, e.updated_at
	FROM catalog.session s
	JOIN catalog.event e ON e.id = s.event_id AND e.city = s.city
	JOIN catalog.place p ON p.id = e.place_id AND p.city = e.city
	WHERE s.city = $1
		AND p.is_active
		AND e.is_active
		AND ((s.starts_at < $3 AND s.ends_at > $2) OR s.id = ANY($4::uuid[]))
	ORDER BY s.starts_at, s.id`

const sessionPriceOffersSQL = `
	SELECT id, session_id, price_status, audience,
		eligibility_age_min, eligibility_age_max,
		amount_min, amount_max, currency, benefit_programs,
		purchase_url, source_record_id, observed_at, valid_until, data_mode
	FROM catalog.price_offer
	WHERE city = $1 AND is_active AND session_id = ANY($2::uuid[])
	ORDER BY session_id, id`

type loadedPlace struct {
	place     domain.Place
	rules     domain.OpeningRules
	baseScore float64
}

// Candidates loads the candidate pool and freshness metadata of a city for the requested interval.
func (c *Catalog) Candidates(ctx context.Context, req domain.OptimizeRequest) ([]domain.Candidate, domain.DataFreshness, error) {
	if err := req.Validate(); err != nil {
		return nil, domain.DataFreshness{}, fmt.Errorf("%w: %v", usecase.ErrInvalidRequest, err)
	}
	if c.db == nil {
		return nil, domain.DataFreshness{}, usecase.ErrUnavailable
	}
	loc, err := time.LoadLocation(req.Timezone)
	if err != nil {
		return nil, domain.DataFreshness{}, fmt.Errorf("%w: %v", usecase.ErrInvalidRequest, err)
	}

	var cityTZ string
	var revision int64
	var cityUpdated time.Time
	err = c.db.QueryRow(ctx, cityCatalogSQL, req.City).Scan(&cityTZ, &revision, &cityUpdated)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.DataFreshness{}, usecase.ErrCatalogNotReady
	}
	if err != nil {
		return nil, domain.DataFreshness{}, wrapDBError("read city catalog", err)
	}
	if revision <= 0 {
		return nil, domain.DataFreshness{}, usecase.ErrCatalogNotReady
	}
	if cityTZ != req.Timezone {
		return nil, domain.DataFreshness{}, fmt.Errorf("%w: city %q timezone is %s, got %s", usecase.ErrInvalidRequest, req.City, cityTZ, req.Timezone)
	}

	places, placeByID, err := c.loadPlaces(ctx, req.City)
	if err != nil {
		return nil, domain.DataFreshness{}, err
	}
	entrancesByPlace, err := c.loadEntrances(ctx, req.City)
	if err != nil {
		return nil, domain.DataFreshness{}, err
	}

	var candidates []domain.Candidate
	for _, lp := range places {
		if lp.place.Category == nil || lp.rules.IsEmpty() {
			continue
		}
		windows, err := lp.rules.Windows(req.Start, req.End, loc, domain.DefaultPlaceMinDuration, domain.DefaultPlaceRecommendedDuration)
		if err != nil {
			return nil, domain.DataFreshness{}, fmt.Errorf("expand opening rules for place %x: %w", lp.place.ID, err)
		}
		for _, w := range windows {
			candidates = append(candidates, domain.Candidate{
				Place:     lp.place,
				Window:    w,
				Entrances: entrancesByPlace[lp.place.ID],
				BaseScore: lp.baseScore,
			})
		}
	}

	sessionCandidates, err := c.loadSessionCandidates(ctx, req, placeByID, entrancesByPlace)
	if err != nil {
		return nil, domain.DataFreshness{}, err
	}
	candidates = append(candidates, sessionCandidates...)

	freshness := buildFreshness(revision, cityUpdated.UTC(), candidates, places)
	return candidates, freshness, nil
}

func (c *Catalog) loadPlaces(ctx context.Context, city string) ([]loadedPlace, map[domain.PlaceID]loadedPlace, error) {
	rows, err := c.db.Query(ctx, activePlacesSQL, city)
	if err != nil {
		return nil, nil, wrapDBError("query active places", err)
	}
	defer rows.Close()

	var list []loadedPlace
	byID := make(map[domain.PlaceID]loadedPlace)
	for rows.Next() {
		var (
			id        domain.PlaceID
			title     string
			category  *string
			tagMask   int64
			lon, lat  float64
			rawRules  []byte
			dataMode  string
			recordID  *domain.SourceRecordID
			updatedAt time.Time
			baseScore float64
		)
		if err := rows.Scan(&id, &title, &category, &tagMask, &lon, &lat, &rawRules, &dataMode, &recordID, &updatedAt, &baseScore); err != nil {
			return nil, nil, wrapDBError("scan place", err)
		}
		rules, err := domain.ParseOpeningRules(rawRules)
		if err != nil {
			return nil, nil, fmt.Errorf("parse opening rules for place %x: %w", id, err)
		}
		var cat *domain.Category
		if category != nil {
			v := domain.Category(*category)
			cat = &v
		}
		p := domain.Place{
			ID:           id,
			City:         city,
			Title:        title,
			Category:     cat,
			InterestMask: domain.InterestMask(uint64(tagMask)),
			Location:     domain.Coordinate{Longitude: lon, Latitude: lat},
			DataMode:     domain.DataMode(dataMode),
			Provenance: domain.Provenance{
				SourceName:     dataMode,
				SourceRecordID: recordID,
				FetchedAt:      updatedAt.UTC(),
			},
		}
		lp := loadedPlace{place: p, rules: rules, baseScore: baseScore}
		list = append(list, lp)
		byID[id] = lp
	}
	if err := rows.Err(); err != nil {
		return nil, nil, wrapDBError("iterate active places", err)
	}
	return list, byID, nil
}

func (c *Catalog) loadEntrances(ctx context.Context, city string) (map[domain.PlaceID][]domain.Entrance, error) {
	rows, err := c.db.Query(ctx, placeEntrancesSQL, city)
	if err != nil {
		return nil, wrapDBError("query place entrances", err)
	}
	defer rows.Close()

	byPlace := make(map[domain.PlaceID][]domain.Entrance)
	for rows.Next() {
		var (
			id            domain.EntranceID
			placeID       domain.PlaceID
			lon, lat      float64
			rawModes      []string
			accessibility string
			verification  string
		)
		if err := rows.Scan(&id, &placeID, &lon, &lat, &rawModes, &accessibility, &verification); err != nil {
			return nil, wrapDBError("scan place entrance", err)
		}
		modes := make([]domain.MovementMode, len(rawModes))
		for i, m := range rawModes {
			modes[i] = domain.MovementMode(m)
		}
		byPlace[placeID] = append(byPlace[placeID], domain.Entrance{
			ID:            id,
			PlaceID:       placeID,
			Location:      domain.Coordinate{Longitude: lon, Latitude: lat},
			AllowedModes:  modes,
			Accessibility: domain.Accessibility(accessibility),
			Verification:  domain.VerificationStatus(verification),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, wrapDBError("iterate place entrances", err)
	}
	return byPlace, nil
}

func (c *Catalog) loadSessionCandidates(
	ctx context.Context,
	req domain.OptimizeRequest,
	placeByID map[domain.PlaceID]loadedPlace,
	entrancesByPlace map[domain.PlaceID][]domain.Entrance,
) ([]domain.Candidate, error) {
	obligatedSet := make(map[domain.SessionID]struct{}, len(req.Constraints.Obligations))
	obligatedUUIDs := make([]string, 0, len(req.Constraints.Obligations))
	for _, o := range req.Constraints.Obligations {
		if o.SessionID == nil {
			continue
		}
		if _, exists := obligatedSet[*o.SessionID]; exists {
			continue
		}
		obligatedSet[*o.SessionID] = struct{}{}
		obligatedUUIDs = append(obligatedUUIDs, formatUUID(*o.SessionID))
	}

	rows, err := c.db.Query(ctx, activeSessionsSQL, req.City, req.Start.UTC(), req.End.UTC(), obligatedUUIDs)
	if err != nil {
		return nil, wrapDBError("query active sessions", err)
	}
	defer rows.Close()

	var candidates []domain.Candidate
	var sessionUUIDs []string
	for rows.Next() {
		var (
			sessionID              domain.SessionID
			eventID                domain.EventID
			slotType               string
			startsAt, endsAt       time.Time
			minDurS, recDurS, bufS int64
			lastEntryAt            *time.Time
			lateEntryAllowed       *bool
			regDeadline            *time.Time
			accessType             string
			availability           string
			availabilityObservedAt *time.Time
			isHard                 bool
			bookingURL             *string
			sessionDataMode        string
			sessionRecordID        *domain.SourceRecordID
			version                int64
			sessionUpdatedAt       time.Time

			placeID       domain.PlaceID
			eventTitle    string
			eventCategory string
			eventTagMask  int64
			ageMin        *int
			ageMax        *int
			eventDataMode string
			eventRecordID *domain.SourceRecordID
			eventUpdated  time.Time
		)
		err := rows.Scan(
			&sessionID, &eventID, &slotType, &startsAt, &endsAt,
			&minDurS, &recDurS, &bufS,
			&lastEntryAt, &lateEntryAllowed, &regDeadline,
			&accessType, &availability, &availabilityObservedAt,
			&isHard, &bookingURL, &sessionDataMode, &sessionRecordID,
			&version, &sessionUpdatedAt,
			&placeID, &eventTitle, &eventCategory, &eventTagMask,
			&ageMin, &ageMax, &eventDataMode, &eventRecordID, &eventUpdated,
		)
		if err != nil {
			return nil, wrapDBError("scan session", err)
		}
		lp, ok := placeByID[placeID]
		if !ok {
			continue
		}

		kind, err := mapWindowKind(slotType)
		if err != nil {
			return nil, err
		}
		window := domain.VisitWindow{
			Kind:                kind,
			Start:               startsAt.UTC(),
			End:                 endsAt.UTC(),
			LastEntryAt:         utcTimePtr(lastEntryAt),
			MinDuration:         time.Duration(minDurS) * time.Second,
			RecommendedDuration: time.Duration(recDurS) * time.Second,
			ArrivalBuffer:       time.Duration(bufS) * time.Second,
			LateEntryAllowed:    lateEntryAllowed,
		}
		_, isObligated := obligatedSet[sessionID]
		if !isObligated && overlapDuration(window.Start, window.End, req.Start.UTC(), req.End.UTC()) < window.MinDuration {
			continue
		}

		event := &domain.Event{
			ID:           eventID,
			PlaceID:      placeID,
			Title:        eventTitle,
			Category:     domain.Category(eventCategory),
			InterestMask: domain.InterestMask(uint64(eventTagMask)),
			AgeMin:       ageMin,
			AgeMax:       ageMax,
			DataMode:     domain.DataMode(eventDataMode),
			Provenance: domain.Provenance{
				SourceName:     eventDataMode,
				SourceRecordID: eventRecordID,
				FetchedAt:      eventUpdated.UTC(),
			},
		}
		fetchedAt := sessionUpdatedAt.UTC()
		if availabilityObservedAt != nil {
			fetchedAt = availabilityObservedAt.UTC()
		}
		session := &domain.Session{
			ID:                     sessionID,
			EventID:                eventID,
			Window:                 window,
			RegistrationDeadline:   utcTimePtr(regDeadline),
			Access:                 domain.AccessType(accessType),
			Availability:           domain.Availability(availability),
			AvailabilityObservedAt: utcTimePtr(availabilityObservedAt),
			IsHard:                 isHard,
			Version:                version,
			DataMode:               domain.DataMode(sessionDataMode),
			Provenance: domain.Provenance{
				SourceName:     sessionDataMode,
				SourceURL:      trimmedStringPtr(bookingURL),
				SourceRecordID: sessionRecordID,
				FetchedAt:      fetchedAt,
			},
		}
		candidates = append(candidates, domain.Candidate{
			Place:     lp.place,
			Event:     event,
			Session:   session,
			Window:    window,
			Entrances: entrancesByPlace[placeID],
			BaseScore: lp.baseScore,
		})
		sessionUUIDs = append(sessionUUIDs, formatUUID(sessionID))
	}
	if err := rows.Err(); err != nil {
		return nil, wrapDBError("iterate active sessions", err)
	}
	if len(sessionUUIDs) == 0 {
		return candidates, nil
	}

	offersBySession, err := c.loadPriceOffers(ctx, req.City, sessionUUIDs)
	if err != nil {
		return nil, err
	}
	for i := range candidates {
		if s := candidates[i].Session; s != nil {
			candidates[i].Offers = offersBySession[s.ID]
		}
	}
	return candidates, nil
}

func (c *Catalog) loadPriceOffers(ctx context.Context, city string, sessionUUIDs []string) (map[domain.SessionID][]domain.PriceOffer, error) {
	rows, err := c.db.Query(ctx, sessionPriceOffersSQL, city, sessionUUIDs)
	if err != nil {
		return nil, wrapDBError("query session price offers", err)
	}
	defer rows.Close()

	bySession := make(map[domain.SessionID][]domain.PriceOffer, len(sessionUUIDs))
	for rows.Next() {
		var (
			id                domain.PriceOfferID
			sessionID         domain.SessionID
			status            string
			audience          string
			eligibilityAgeMin *int
			eligibilityAgeMax *int
			amountMin         *int64
			amountMax         *int64
			currency          *string
			benefitPrograms   []string
			purchaseURL       *string
			recordID          domain.SourceRecordID
			observedAt        time.Time
			validUntil        *time.Time
			dataMode          string
		)
		err := rows.Scan(
			&id, &sessionID, &status, &audience,
			&eligibilityAgeMin, &eligibilityAgeMax,
			&amountMin, &amountMax, &currency, &benefitPrograms,
			&purchaseURL, &recordID, &observedAt, &validUntil, &dataMode,
		)
		if err != nil {
			return nil, wrapDBError("scan price offer", err)
		}
		curr := defaultCurrency
		if currency != nil {
			if trimmed := strings.TrimSpace(*currency); trimmed != "" {
				curr = trimmed
			}
		}
		rec := recordID
		bySession[sessionID] = append(bySession[sessionID], domain.PriceOffer{
			ID:        id,
			SessionID: sessionID,
			Price: domain.Price{
				Status:     domain.PriceStatus(status),
				Currency:   curr,
				LowerMinor: amountMin,
				UpperMinor: amountMax,
			},
			Audience:          domain.Audience(audience),
			EligibilityAgeMin: eligibilityAgeMin,
			EligibilityAgeMax: eligibilityAgeMax,
			BenefitPrograms:   benefitPrograms,
			ValidUntil:        utcTimePtr(validUntil),
			Provenance: domain.Provenance{
				SourceName:     dataMode,
				SourceURL:      trimmedStringPtr(purchaseURL),
				SourceRecordID: &rec,
				FetchedAt:      observedAt.UTC(),
			},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, wrapDBError("iterate session price offers", err)
	}
	return bySession, nil
}

func buildFreshness(revision int64, cityUpdated time.Time, candidates []domain.Candidate, places []loadedPlace) domain.DataFreshness {
	mode := domain.DataPrepared
	switch {
	case len(candidates) > 0:
		mode = candidates[0].DataMode()
		for _, c := range candidates[1:] {
			mode = domain.Weakest(mode, c.DataMode())
		}
	case len(places) > 0:
		mode = places[0].place.DataMode
		for _, p := range places[1:] {
			mode = domain.Weakest(mode, p.place.DataMode)
		}
	}
	asOf := cityUpdated
	return domain.DataFreshness{
		DataMode:        mode,
		DataAsOf:        &asOf,
		CatalogRevision: domain.CatalogRevision(revision),
	}
}

func mapWindowKind(slotType string) (domain.WindowKind, error) {
	switch slotType {
	case "FIXED_SESSION":
		return domain.WindowFixed, nil
	case "CONTINUOUS_WINDOW":
		return domain.WindowContinuous, nil
	default:
		return "", fmt.Errorf("unknown session slot type %q", slotType)
	}
}

func overlapDuration(aStart, aEnd, bStart, bEnd time.Time) time.Duration {
	start := aStart
	if bStart.After(start) {
		start = bStart
	}
	end := aEnd
	if bEnd.Before(end) {
		end = bEnd
	}
	if !end.After(start) {
		return 0
	}
	return end.Sub(start)
}

func utcTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func trimmedStringPtr(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}

func formatUUID(id [16]byte) string {
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16])
}

func wrapDBError(op string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%s: %w: %v", op, usecase.ErrUnavailable, err)
}
