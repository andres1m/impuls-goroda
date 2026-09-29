package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/catalogslice"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/usecase"
)

const defaultCurrency = "RUB"

// CatalogDB reads the catalog; slices are read in one transaction so they match one revision.
type CatalogDB interface {
	Querier
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

// Catalog reads places, entrances, events, sessions and price offers from the city catalog.
type Catalog struct {
	db CatalogDB
}

func NewCatalog(db CatalogDB) *Catalog {
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
		AND e.is_active`

const (
	sessionsAfterSQL = activeSessionsSQL + `
		AND s.ends_at > $2
	ORDER BY s.starts_at, s.id`
	sessionsByIDSQL = activeSessionsSQL + `
		AND s.id = ANY($2::uuid[])
	ORDER BY s.starts_at, s.id`
)

const sessionPriceOffersSQL = `
	SELECT id, session_id, price_status, audience,
		eligibility_age_min, eligibility_age_max,
		amount_min, amount_max, currency, benefit_programs,
		purchase_url, source_record_id, observed_at, valid_until, data_mode
	FROM catalog.price_offer
	WHERE city = $1 AND is_active AND session_id = ANY($2::uuid[])
	ORDER BY session_id, id`

// Candidates reads the candidate pool of the request straight from the database. The day comes from
// one snapshot; commitments to sessions that ended before it are read afterwards.
func (c *Catalog) Candidates(
	ctx context.Context,
	req *domain.OptimizeRequest,
) ([]domain.Candidate, domain.DataFreshness, error) {
	if err := req.Validate(); err != nil {
		return nil, domain.DataFreshness{}, fmt.Errorf("%w: %w", usecase.ErrInvalidRequest, err)
	}
	if _, err := time.LoadLocation(req.Timezone); err != nil {
		return nil, domain.DataFreshness{}, fmt.Errorf("%w: %w", usecase.ErrInvalidRequest, err)
	}
	slice, err := c.LoadSlice(ctx, req.City, req.Start)
	if err != nil {
		return nil, domain.DataFreshness{}, err
	}
	var extra []domain.Candidate
	if missing := slice.Missing(req); len(missing) > 0 {
		if extra, err = c.SessionsByID(ctx, slice, missing); err != nil {
			return nil, domain.DataFreshness{}, err
		}
	}
	return slice.Candidates(req, extra)
}

// Revision is the city's published catalog revision; a city without one has no catalog yet.
func (c *Catalog) Revision(ctx context.Context, city string) (domain.CatalogRevision, error) {
	if c.db == nil {
		return 0, usecase.ErrUnavailable
	}
	var revision int64
	err := c.db.QueryRow(ctx, `SELECT catalog_revision FROM ref.city WHERE code = $1`, city).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, usecase.ErrCatalogNotReady
	}
	if err != nil {
		return 0, wrapDBError("read catalog revision", err)
	}
	if revision <= 0 {
		return 0, usecase.ErrCatalogNotReady
	}
	return domain.CatalogRevision(revision), nil
}

// LoadSlice reads the city's catalog as one revision left it, with the sessions that end after horizon.
func (c *Catalog) LoadSlice(ctx context.Context, city string, horizon time.Time) (*catalogslice.Slice, error) {
	if c.db == nil {
		return nil, usecase.ErrUnavailable
	}
	tx, err := c.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, wrapDBError("begin catalog snapshot", err)
	}
	defer func() {
		// Rollback also runs after a successful commit; ErrTxClosed needs no action.
		_ = tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck // best-effort cleanup after commit
	}()

	slice := &catalogslice.Slice{City: city, Horizon: horizon.UTC(), BuiltAt: time.Now().UTC()}
	var revision int64
	err = tx.QueryRow(ctx, cityCatalogSQL, city).Scan(&slice.Timezone, &revision, &slice.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, usecase.ErrCatalogNotReady
	}
	if err != nil {
		return nil, wrapDBError("read city catalog", err)
	}
	if revision <= 0 {
		return nil, usecase.ErrCatalogNotReady
	}
	slice.Revision, slice.UpdatedAt = domain.CatalogRevision(revision), slice.UpdatedAt.UTC()

	if slice.Places, err = loadPlaces(ctx, tx, city); err != nil {
		return nil, err
	}
	entrances, err := loadEntrances(ctx, tx, city)
	if err != nil {
		return nil, err
	}
	for i := range slice.Places {
		slice.Places[i].Entrances = entrances[slice.Places[i].Place.ID]
	}
	if slice.Sessions, err = loadSessions(ctx, tx, slice, sessionsAfterSQL, horizon.UTC()); err != nil {
		return nil, err
	}
	return slice, nil
}

// SessionsByID reads sessions the slice does not hold, such as a commitment to one that already ended,
// with the places the slice knows.
func (c *Catalog) SessionsByID(
	ctx context.Context,
	slice *catalogslice.Slice,
	ids []domain.SessionID,
) ([]domain.Candidate, error) {
	if c.db == nil {
		return nil, usecase.ErrUnavailable
	}
	uuids := make([]string, len(ids))
	for i, id := range ids {
		uuids[i] = formatUUID(id)
	}
	return loadSessions(ctx, c.db, slice, sessionsByIDSQL, uuids)
}

func loadPlaces(ctx context.Context, q Querier, city string) ([]catalogslice.Place, error) {
	rows, err := q.Query(ctx, activePlacesSQL, city)
	if err != nil {
		return nil, wrapDBError("query active places", err)
	}
	defer rows.Close()

	var list []catalogslice.Place
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
		if err := rows.Scan(
			&id,
			&title,
			&category,
			&tagMask,
			&lon,
			&lat,
			&rawRules,
			&dataMode,
			&recordID,
			&updatedAt,
			&baseScore,
		); err != nil {
			return nil, wrapDBError("scan place", err)
		}
		rules, err := domain.ParseOpeningRules(rawRules)
		if err != nil {
			return nil, fmt.Errorf("parse opening rules for place %x: %w", id, err)
		}
		var cat *domain.Category
		if category != nil {
			v := domain.Category(*category)
			cat = &v
		}
		list = append(list, catalogslice.Place{
			Place: domain.Place{
				ID:           id,
				City:         city,
				Title:        title,
				Category:     cat,
				InterestMask: reinterpretMask(tagMask),
				Location:     domain.Coordinate{Longitude: lon, Latitude: lat},
				DataMode:     domain.DataMode(dataMode),
				Provenance: domain.Provenance{
					SourceName:     dataMode,
					SourceRecordID: recordID,
					FetchedAt:      updatedAt.UTC(),
				},
			},
			Rules:     rules,
			BaseScore: baseScore,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, wrapDBError("iterate active places", err)
	}
	return list, nil
}

func loadEntrances(ctx context.Context, q Querier, city string) (map[domain.PlaceID][]domain.Entrance, error) {
	rows, err := q.Query(ctx, placeEntrancesSQL, city)
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

// loadSessions reads session candidates by one of the session queries; sessions of places the slice
// does not hold are left out.
//
//nolint:funlen // one row scan must keep the session and offer fields aligned
func loadSessions(
	ctx context.Context,
	q Querier,
	slice *catalogslice.Slice,
	sql string,
	filter any,
) ([]domain.Candidate, error) {
	rows, queryErr := q.Query(ctx, sql, slice.City, filter)
	if queryErr != nil {
		return nil, wrapDBError("query active sessions", queryErr)
	}
	defer rows.Close()

	places := make(map[domain.PlaceID]catalogslice.Place, len(slice.Places))
	for i := range slice.Places {
		places[slice.Places[i].Place.ID] = slice.Places[i]
	}
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
		place, ok := places[placeID]
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
		event := &domain.Event{
			ID:           eventID,
			PlaceID:      placeID,
			Title:        eventTitle,
			Category:     domain.Category(eventCategory),
			InterestMask: reinterpretMask(eventTagMask),
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
			Place:     place.Place,
			Event:     event,
			Session:   session,
			Window:    window,
			Entrances: place.Entrances,
			BaseScore: place.BaseScore,
		})
		sessionUUIDs = append(sessionUUIDs, formatUUID(sessionID))
	}
	if err := rows.Err(); err != nil {
		return nil, wrapDBError("iterate active sessions", err)
	}
	if len(sessionUUIDs) == 0 {
		return candidates, nil
	}

	offersBySession, err := loadPriceOffers(ctx, q, slice.City, sessionUUIDs)
	if err != nil {
		return nil, err
	}
	for i := range candidates {
		candidates[i].Offers = offersBySession[candidates[i].Session.ID]
	}
	return candidates, nil
}

func loadPriceOffers(
	ctx context.Context,
	q Querier,
	city string,
	sessionUUIDs []string,
) (map[domain.SessionID][]domain.PriceOffer, error) {
	rows, err := q.Query(ctx, sessionPriceOffersSQL, city, sessionUUIDs)
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
	return fmt.Errorf("%s: %w: %w", op, usecase.ErrUnavailable, err)
}

// reinterpretMask preserves the 64-bit bit pattern stored in a signed PostgreSQL bigint.
func reinterpretMask(mask int64) domain.InterestMask {
	return domain.InterestMask(uint64(mask)) //nolint:gosec // the signed representation is intentional
}
