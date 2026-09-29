package seed

import (
	"fmt"
	"math"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/normalize"
	"github.com/google/uuid"
	"github.com/uber/h3-go/v4"
)

const (
	h3ResCoarse     = 8
	h3ResFine       = 11
	kopecksPerRuble = 100
)

var timezones = map[string]string{"moscow": "Europe/Moscow", "perm": "Asia/Yekaterinburg"}

type Rows struct {
	City     string
	Version  string
	Places   []PlaceRow
	Events   []EventRow
	Sessions []SessionRow
	Prices   []PriceRow
}

type PlaceRow struct {
	ID              uuid.UUID
	ExternalID      string
	Title           string
	NormalizedTitle string
	Category        *string
	TagMask         int64
	Lat, Lon        float64
	Address         *string
	OpeningRules    []byte
	H3Res8, H3Res11 int64
}

type EventRow struct {
	ID, PlaceID     uuid.UUID
	ExternalID      string
	Title           string
	NormalizedTitle string
	Category        string
	TagMask         int64
	Organizer       *string
	AgeMin          *int16
}

type SessionRow struct {
	ID, EventID          uuid.UUID
	ExternalID           string
	SlotType             string
	StartsAt, EndsAt     time.Time
	MinDurationS         int32
	RecommendedDurationS int32
	BufferS              int32
	LastEntryAt          *time.Time
	LateEntryAllowed     *bool
	RegistrationDeadline *time.Time
	AccessType           string
	Availability         string
	CancellationReason   *string
	IsHard               bool
}

type PriceRow struct {
	ID, SessionID     uuid.UUID
	Status            string
	Audience          string
	TariffLabel       *string
	EligibilityAgeMin *int16
	EligibilityAgeMax *int16
	AmountMin         *int64
	AmountMax         *int64
	Currency          *string
	BenefitPrograms   []string
}

// Expand checks the dataset and lays its sessions out over the horizon starting at from,
// a YYYY-MM-DD date in the city's calendar; an empty from means the city's current date.
func Expand(ds *Dataset, ref Reference, from string, now time.Time) (Rows, error) {
	location, err := cityLocation(ds.City)
	if err != nil {
		return Rows{}, err
	}
	if valErr := ds.validate(ref); valErr != nil {
		return Rows{}, valErr
	}
	firstDay, err := startDate(from, now.In(location))
	if err != nil {
		return Rows{}, err
	}

	rows := Rows{City: ds.City, Version: ds.Version}
	placeIDs := make(map[string]uuid.UUID, len(ds.Places))
	for i := range ds.Places {
		p := &ds.Places[i]
		row, placeErr := expandPlace(ds.City, p, ref)
		if placeErr != nil {
			return Rows{}, fmt.Errorf("place %q: %w", p.Key, placeErr)
		}
		placeIDs[p.Key] = row.ID
		rows.Places = append(rows.Places, row)
	}
	for i := range ds.Events {
		e := &ds.Events[i]
		event, evErr := expandEvent(ds.City, e, placeIDs[e.Place], ref)
		if evErr != nil {
			return Rows{}, fmt.Errorf("event %q: %w", e.Key, evErr)
		}
		rows.Events = append(rows.Events, event)
		if sessErr := expandEventSessions(ds.City, e, event.ID, firstDay, location, &rows); sessErr != nil {
			return Rows{}, sessErr
		}
	}
	return rows, nil
}

func expandEventSessions(
	city string,
	e *Event,
	eventID uuid.UUID,
	firstDay time.Time,
	location *time.Location,
	rows *Rows,
) error {
	for j := range e.Sessions {
		s := &e.Sessions[j]
		for _, day := range sessionDays(s) {
			session, sessErr := expandSession(city, e.Key, eventID, s, firstDay.AddDate(0, 0, day), location)
			if sessErr != nil {
				return fmt.Errorf("session %q: %w", s.Key, sessErr)
			}
			rows.Sessions = append(rows.Sessions, session)
			rows.Prices = append(rows.Prices, expandPrices(&session, s)...)
		}
	}
	return nil
}

func cityLocation(city string) (*time.Location, error) {
	name, ok := timezones[city]
	if !ok {
		return nil, fmt.Errorf("unknown city %q", city)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("load location %s: %w", name, err)
	}
	return loc, nil
}

// startDate returns the first calendar day as midnight UTC; only its year, month and day are used.
func startDate(from string, localNow time.Time) (time.Time, error) {
	if from == "" {
		return time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, time.UTC), nil
	}
	date, err := time.Parse(time.DateOnly, from)
	if err != nil {
		return time.Time{}, fmt.Errorf("start date %q is not YYYY-MM-DD", from)
	}
	return date, nil
}

func sessionDays(s *Session) []int {
	if len(s.Days) > 0 {
		return s.Days
	}
	days := make([]int, horizonDays)
	for i := range days {
		days[i] = i
	}
	return days
}

func expandPlace(city string, p *Place, ref Reference) (PlaceRow, error) {
	externalID := "place:" + city + ":" + p.Key
	mask, err := tagMask(p.Tags, ref)
	if err != nil {
		return PlaceRow{}, err
	}
	rules := []byte("{}")
	if p.OpeningRules != nil {
		if rules, err = p.OpeningRules.MarshalJSON(); err != nil {
			return PlaceRow{}, err
		}
	}
	point := h3.NewLatLng(p.Lat, p.Lon)
	res8, err := h3.LatLngToCell(point, h3ResCoarse)
	if err != nil {
		return PlaceRow{}, fmt.Errorf("h3 cell: %w", err)
	}
	res11, err := h3.LatLngToCell(point, h3ResFine)
	if err != nil {
		return PlaceRow{}, fmt.Errorf("h3 cell: %w", err)
	}
	return PlaceRow{
		ID:              normalize.EntityID(externalID),
		ExternalID:      externalID,
		Title:           p.Title,
		NormalizedTitle: normalize.NormalizedTitle(p.Title),
		Category:        optional(p.Category),
		TagMask:         mask,
		Lat:             p.Lat,
		Lon:             p.Lon,
		Address:         optional(p.Address),
		OpeningRules:    rules,
		H3Res8:          int64(res8),
		H3Res11:         int64(res11),
	}, nil
}

func expandEvent(city string, e *Event, placeID uuid.UUID, ref Reference) (EventRow, error) {
	externalID := "event:" + city + ":" + e.Key
	mask, err := tagMask(e.Tags, ref)
	if err != nil {
		return EventRow{}, err
	}
	var ageMin *int16
	if e.AgeMin != nil {
		age := clampInt16(*e.AgeMin)
		ageMin = &age
	}
	return EventRow{
		ID:              normalize.EntityID(externalID),
		PlaceID:         placeID,
		ExternalID:      externalID,
		Title:           e.Title,
		NormalizedTitle: normalize.NormalizedTitle(e.Title),
		Category:        e.Category,
		TagMask:         mask,
		Organizer:       optional(e.Organizer),
		AgeMin:          ageMin,
	}, nil
}

func expandSession(
	city, eventKey string,
	eventID uuid.UUID,
	s *Session,
	date time.Time,
	location *time.Location,
) (SessionRow, error) {
	at := func(clock string) (time.Time, error) {
		minutes, err := normalize.ClockMinutes(clock, true)
		if err != nil {
			return time.Time{}, err
		}
		return time.Date(date.Year(), date.Month(), date.Day(), 0, minutes, 0, 0, location), nil
	}
	startsAt, err := at(s.Start)
	if err != nil {
		return SessionRow{}, err
	}
	endsAt, err := at(s.End)
	if err != nil {
		return SessionRow{}, err
	}
	externalID := fmt.Sprintf("session:%s:%s:%s:%s", city, eventKey, s.Key, date.Format(time.DateOnly))
	recommended := s.RecommendedDuration
	if recommended == 0 {
		recommended = s.MinDuration
	}
	row := SessionRow{
		ID:                   normalize.EntityID(externalID),
		EventID:              eventID,
		ExternalID:           externalID,
		SlotType:             slotTypes[s.Slot],
		StartsAt:             startsAt,
		EndsAt:               endsAt,
		MinDurationS:         durationSeconds(s.MinDuration),
		RecommendedDurationS: durationSeconds(recommended),
		BufferS:              durationSeconds(s.Buffer),
		LateEntryAllowed:     s.LateEntry,
		AccessType:           s.Access,
		Availability:         s.Availability,
		CancellationReason:   optional(s.CancellationReason),
		IsHard:               s.Slot == slotFixed,
	}
	if s.Hard != nil {
		row.IsHard = *s.Hard
	}
	if s.LastEntry != "" {
		last, lastErr := at(s.LastEntry)
		if lastErr != nil {
			return SessionRow{}, lastErr
		}
		row.LastEntryAt = &last
	}
	if s.RegistrationClosesBefore > 0 {
		deadline := row.StartsAt.Add(-s.RegistrationClosesBefore)
		row.RegistrationDeadline = &deadline
	}
	return row, nil
}

func expandPrices(session *SessionRow, s *Session) []PriceRow {
	if len(s.Prices) == 0 {
		return []PriceRow{priceRow(session, &Price{Audience: "general", Status: statusFree})}
	}
	rows := make([]PriceRow, 0, len(s.Prices))
	for i := range s.Prices {
		rows = append(rows, priceRow(session, &s.Prices[i]))
	}
	return rows
}

func priceRow(session *SessionRow, p *Price) PriceRow {
	row := PriceRow{
		ID:              normalize.EntityID(session.ExternalID + ":price:" + p.Audience),
		SessionID:       session.ID,
		Status:          p.Status,
		Audience:        p.Audience,
		TariffLabel:     optional(p.TariffLabel),
		BenefitPrograms: append([]string{}, p.BenefitPrograms...),
	}
	switch p.Status {
	case statusFree:
		row.AmountMin, row.AmountMax = kopecks(0), kopecks(0)
	case statusFixed:
		row.AmountMin, row.AmountMax = kopecks(p.Amount[0]), kopecks(p.Amount[0])
	case statusRange:
		row.AmountMin, row.AmountMax = kopecks(p.Amount[0]), kopecks(p.Amount[1])
	}
	if row.AmountMin != nil {
		currency := "RUB"
		row.Currency = &currency
	}
	if len(p.EligibilityAge) == eligibilityAgeBoundsLen {
		lower, upper := clampInt16(p.EligibilityAge[0]), clampInt16(p.EligibilityAge[1])
		row.EligibilityAgeMin, row.EligibilityAgeMax = &lower, &upper
	}
	return row
}

func durationSeconds(d time.Duration) int32 {
	sec := int64(d / time.Second)
	if sec < math.MinInt32 || sec > math.MaxInt32 {
		return 0
	}
	return int32(sec)
}

func clampInt16(v int) int16 {
	if v < math.MinInt16 || v > math.MaxInt16 {
		return 0
	}
	return int16(v)
}

func kopecks(rubles int64) *int64 {
	amount := rubles * kopecksPerRuble
	return &amount
}

func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
