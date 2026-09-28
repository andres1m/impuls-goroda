package normalize

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

func kudagoDate(start, end int64) map[string]any {
	return map[string]any{"start": start, "end": end, "is_startless": false, "is_endless": false,
		"use_place_schedule": false, "schedules": []any{}}
}

func kudagoPayload(t *testing.T, edit func(e map[string]any)) []byte {
	t.Helper()
	at := now.Unix()
	e := map[string]any{
		"id": 172178, "title": "большой стендап-концерт", "short_title": "Большой стендап-концерт",
		"site_url": "https://kudago.com/msk/event/x/", "categories": []any{"entertainment", "concert"},
		"age_restriction": "18+", "price": "от 600 до 1000 рублей", "is_free": false,
		"place": map[string]any{"id": 33778, "title": "Клуб", "address": "ул. Тверская, 1", "is_closed": false,
			"is_stub": true, "coords": map[string]any{"lat": 55.76, "lon": 37.61}},
		"dates": []any{
			kudagoDate(at+3600, at+3*3600),
			kudagoDate(at+7200, at+7200),
			map[string]any{"start": at, "end": at + 3600, "is_startless": false, "is_endless": false,
				"use_place_schedule": false, "schedules": []any{map[string]any{"days_of_week": []any{3}}}},
			map[string]any{"start": at, "end": at + 3600, "is_startless": false, "is_endless": false,
				"use_place_schedule": true, "schedules": []any{}},
			map[string]any{"start": -62135433000, "end": at + 3600, "is_startless": true, "is_endless": false,
				"use_place_schedule": false, "schedules": []any{}},
			map[string]any{"start": at, "end": at + 3600, "is_startless": false, "is_endless": true,
				"use_place_schedule": false, "schedules": []any{}},
			kudagoDate(at-7200, at-3600),
		},
	}
	if edit != nil {
		edit(e)
	}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func kudagoCode(t *testing.T, payload []byte) string {
	t.Helper()
	_, err := KudaGoEvent(domain.Moscow, "event:172178", payload, now)
	var bad *DataError
	if !errors.As(err, &bad) {
		t.Fatalf("error %v is not a data error", err)
	}
	return bad.Code
}

func TestKudaGoEvent(t *testing.T) {
	d, err := KudaGoEvent(domain.Moscow, "event:172178", kudagoPayload(t, nil), now)
	if err != nil {
		t.Fatal(err)
	}
	if d.Place.ExternalID != "place:33778" || d.Place.Title != "Клуб" || *d.Place.Address != "ул. Тверская, 1" ||
		d.Place.Lat != 55.76 || d.Place.Lon != 37.61 || d.Place.Category != "" || string(d.Place.OpeningRules) != "{}" {
		t.Fatalf("place %+v", d.Place)
	}
	e := d.Event
	if e.ExternalID != "event:172178@place:33778" || e.Title != "Большой стендап-концерт" || e.Category != "culture" ||
		!slices.Equal(e.Tags, []string{"performing_arts"}) || *e.AgeMin != 18 || e.Organizer != nil {
		t.Fatalf("event %+v", e)
	}
	if len(e.Sessions) != 1 {
		t.Fatalf("only the explicit future date is a session: %+v", e.Sessions)
	}
	s := e.Sessions[0]
	if s.SlotType != "FIXED_SESSION" || s.StartsAt.Unix() != now.Unix()+3600 || s.Price.Status != "range" ||
		*s.BookingURL != "https://kudago.com/msk/event/x/" {
		t.Fatalf("session %+v", s)
	}
}

func TestKudaGoCategories(t *testing.T) {
	for name, tc := range map[string]struct {
		categories []any
		category   string
		tags       []string
	}{
		"exhibition":        {[]any{"exhibition"}, "culture", nil},
		"theater":           {[]any{"theater"}, "culture", []string{"performing_arts"}},
		"education":         {[]any{"education"}, "culture", []string{"lectures_workshops"}},
		"cinema":            {[]any{"cinema"}, "culture", []string{"cinema"}},
		"tour":              {[]any{"tour"}, "tourism", []string{"excursions"}},
		"festival":          {[]any{"festival"}, "culture", nil},
		"table order wins":  {[]any{"tour", "exhibition"}, "culture", []string{"excursions"}},
		"interests are all": {[]any{"education", "cinema"}, "culture", []string{"cinema", "lectures_workshops"}},
	} {
		d, err := KudaGoEvent(domain.Moscow, "event:1", kudagoPayload(t, func(e map[string]any) { e["categories"] = tc.categories }), now)
		if err != nil || d.Event.Category != tc.category || !slices.Equal(d.Event.Tags, tc.tags) {
			t.Errorf("%s: %+v %v", name, d.Event, err)
		}
	}
	if code := kudagoCode(t, kudagoPayload(t, func(e map[string]any) { e["categories"] = []any{"entertainment", "party", "kids"} })); code != "unmapped_category" {
		t.Fatalf("unmapped: %s", code)
	}
}

func TestKudaGoPlaceAndTitle(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(e map[string]any)
		code string
	}{
		"no place":     {func(e map[string]any) { e["place"] = nil }, "missing_place"},
		"closed place": {func(e map[string]any) { e["place"].(map[string]any)["is_closed"] = true }, "missing_place"},
		"null lat": {func(e map[string]any) {
			e["place"].(map[string]any)["coords"] = map[string]any{"lat": nil, "lon": 37.6}
		}, "bad_coordinates"},
		"far away": {func(e map[string]any) {
			e["place"].(map[string]any)["coords"] = map[string]any{"lat": 59.93, "lon": 30.33}
		}, "bad_coordinates"},
		"no titles": {func(e map[string]any) { e["title"] = " "; e["short_title"] = "" }, "missing_name"},
		"broken":    {nil, "bad_payload"},
	} {
		payload := []byte(`{"place":`)
		if tc.edit != nil {
			payload = kudagoPayload(t, tc.edit)
		}
		if code := kudagoCode(t, payload); code != tc.code {
			t.Errorf("%s: %s", name, code)
		}
	}
	d, err := KudaGoEvent(domain.Moscow, "event:1", kudagoPayload(t, func(e map[string]any) { e["short_title"] = "" }), now)
	if err != nil || d.Event.Title != "большой стендап-концерт" {
		t.Fatalf("falls back to title: %+v %v", d.Event, err)
	}
}

func TestKudaGoWithoutDatesInHorizonKeepsTheEvent(t *testing.T) {
	d, err := KudaGoEvent(domain.Moscow, "event:1", kudagoPayload(t, func(e map[string]any) { e["dates"] = []any{} }), now)
	if err != nil || d.Event == nil || d.Event.Sessions != nil {
		t.Fatalf("event %+v %v", d.Event, err)
	}
}

func TestKudaGoAge(t *testing.T) {
	for raw, want := range map[any]*int16{"18+": ptr16(18), "6+": ptr16(6), 0: ptr16(0), nil: nil, "": nil, "abc": nil, "-3+": nil} {
		d, err := KudaGoEvent(domain.Moscow, "event:1", kudagoPayload(t, func(e map[string]any) { e["age_restriction"] = raw }), now)
		if err != nil {
			t.Fatal(err)
		}
		if got := d.Event.AgeMin; (got == nil) != (want == nil) || (got != nil && *got != *want) {
			t.Errorf("%v: %v", raw, got)
		}
	}
}

func ptr16(v int16) *int16 { return &v }

func TestKudaGoPrice(t *testing.T) {
	for text, want := range map[string]PriceDraft{
		"800 рублей":                  fixedPrice(800),
		"1990 рублей":                 fixedPrice(1990),
		"2500 руб.":                   fixedPrice(2500),
		"от 600 до 1000 рублей":       rangePrice(600, 1000),
		"от 1100 до 11 000 рублей":    rangePrice(1100, 11000),
		"От 590 до 2290 рублей":       rangePrice(590, 2290),
		"от 1000 до 600 рублей":       unknownPrice("от 1000 до 600 рублей"),
		"от 990 рублей":               unknownPrice("от 990 рублей"),
		"от 1000 руб.":                unknownPrice("от 1000 руб."),
		"1500 рублей (при покупке 3)": unknownPrice("1500 рублей (при покупке 3)"),
		"  ": unknownPrice(""),
		"":   unknownPrice(""),
	} {
		got := kudagoPrice(false, text)
		if got.Status != want.Status || !sameAmount(got.AmountMin, want.AmountMin) || !sameAmount(got.AmountMax, want.AmountMax) ||
			(got.TariffLabel == nil) != (want.TariffLabel == nil) || (got.TariffLabel != nil && *got.TariffLabel != *want.TariffLabel) {
			t.Errorf("%q: %+v", text, got)
		}
	}
	if got := kudagoPrice(false, "800 рублей"); *got.AmountMin != 80000 {
		t.Fatalf("kopecks: %+v", got)
	}
	if got := kudagoPrice(false, "99999999999999999 рублей"); got.Status != "unknown" || *got.TariffLabel != "99999999999999999 рублей" {
		t.Fatalf("overflowing sum: %+v", got)
	}
	if got := kudagoPrice(true, "вход бесплатный, депозит на еду — 700 рублей"); got.Status != "free" {
		t.Fatalf("free flag: %+v", got)
	}
}
