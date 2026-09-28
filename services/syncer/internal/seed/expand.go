package seed

import (
	"fmt"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/normalize"
	"github.com/google/uuid"
	"github.com/uber/h3-go/v4"
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
func Expand(ds Dataset, ref Reference, from string, now time.Time) (Rows, error) {
	location, err := cityLocation(ds.City)
	if err != nil {
		return Rows{}, err
	}
	if err := ds.validate(ref); err != nil {
		return Rows{}, err
	}
	firstDay, err := startDate(from, now.In(location))
	if err != nil {
		return Rows{}, err
	}

	rows := Rows{City: ds.City, Version: ds.Version}
	placeIDs := make(map[string]uuid.UUID, len(ds.Places))
	for _, p := range ds.Places {
		row, err := expandPlace(ds.City, p, ref)
		if err != nil {
			return Rows{}, fmt.Errorf("place %q: %w", p.Key, err)
		}
		placeIDs[p.Key] = row.ID
		rows.Places = append(rows.Places, row)
	}
	for _, e := range ds.Events {
		event := expandEvent(ds.City, e, placeIDs[e.Place], ref)
		rows.Events = append(rows.Events, event)
		for _, s := range e.Sessions {
			for _, day := range sessionDays(s) {
				session := expandSession(ds.City, e.Key, event.ID, s, firstDay.AddDate(0, 0, day), location)
				rows.Sessions = append(rows.Sessions, session)
				rows.Prices = append(rows.Prices, expandPrices(session, s)...)
			}
		}
	}
	return rows, nil
}

func cityLocation(city string) (*time.Location, error) {
	name, ok := timezones[city]
	if !ok {
		return nil, fmt.Errorf("unknown city %q", city)
	}
	return time.LoadLocation(name)
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

func sessionDays(s Session) []int {
	if len(s.Days) > 0 {
		return s.Days
	}
	days := make([]int, horizonDays)
	for i := range days {
		days[i] = i
	}
	return days
}

func expandPlace(city string, p Place, ref Reference) (PlaceRow, error) {
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
	res8, err := h3.LatLngToCell(point, 8)
	if err != nil {
		return PlaceRow{}, fmt.Errorf("h3 cell: %w", err)
	}
	res11, err := h3.LatLngToCell(point, 11)
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

func expandEvent(city string, e Event, placeID uuid.UUID, ref Reference) EventRow {
	externalID := "event:" + city + ":" + e.Key
	mask, _ := tagMask(e.Tags, ref) // validated
	var ageMin *int16
	if e.AgeMin != nil {
		age := int16(*e.AgeMin)
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
	}
}

func expandSession(city, eventKey string, eventID uuid.UUID, s Session, date time.Time, location *time.Location) SessionRow {
	at := func(clock string) time.Time {
		minutes, _ := normalize.ClockMinutes(clock, true) // validated
		return time.Date(date.Year(), date.Month(), date.Day(), 0, minutes, 0, 0, location)
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
		StartsAt:             at(s.Start),
		EndsAt:               at(s.End),
		MinDurationS:         int32(s.MinDuration / time.Second),
		RecommendedDurationS: int32(recommended / time.Second),
		BufferS:              int32(s.Buffer / time.Second),
		LateEntryAllowed:     s.LateEntry,
		AccessType:           s.Access,
		Availability:         s.Availability,
		CancellationReason:   optional(s.CancellationReason),
		IsHard:               s.Slot == "fixed",
	}
	if s.Hard != nil {
		row.IsHard = *s.Hard
	}
	if s.LastEntry != "" {
		last := at(s.LastEntry)
		row.LastEntryAt = &last
	}
	if s.RegistrationClosesBefore > 0 {
		deadline := row.StartsAt.Add(-s.RegistrationClosesBefore)
		row.RegistrationDeadline = &deadline
	}
	return row
}

func expandPrices(session SessionRow, s Session) []PriceRow {
	if len(s.Prices) == 0 {
		return []PriceRow{priceRow(session, Price{Audience: "general", Status: "free"})}
	}
	rows := make([]PriceRow, 0, len(s.Prices))
	for _, p := range s.Prices {
		rows = append(rows, priceRow(session, p))
	}
	return rows
}

func priceRow(session SessionRow, p Price) PriceRow {
	row := PriceRow{
		ID:              normalize.EntityID(session.ExternalID + ":price:" + p.Audience),
		SessionID:       session.ID,
		Status:          p.Status,
		Audience:        p.Audience,
		TariffLabel:     optional(p.TariffLabel),
		BenefitPrograms: append([]string{}, p.BenefitPrograms...),
	}
	switch p.Status {
	case "free":
		row.AmountMin, row.AmountMax = kopecks(0), kopecks(0)
	case "fixed":
		row.AmountMin, row.AmountMax = kopecks(p.Amount[0]), kopecks(p.Amount[0])
	case "range":
		row.AmountMin, row.AmountMax = kopecks(p.Amount[0]), kopecks(p.Amount[1])
	}
	if row.AmountMin != nil {
		currency := "RUB"
		row.Currency = &currency
	}
	if len(p.EligibilityAge) == 2 {
		lower, upper := int16(p.EligibilityAge[0]), int16(p.EligibilityAge[1])
		row.EligibilityAgeMin, row.EligibilityAgeMax = &lower, &upper
	}
	return row
}

func kopecks(rubles int64) *int64 {
	amount := rubles * 100
	return &amount
}

func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
