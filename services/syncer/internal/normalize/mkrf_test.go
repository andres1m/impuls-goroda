package normalize

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

func mkrfPayload(t *testing.T, edit func(g map[string]any)) []byte {
	t.Helper()
	g := map[string]any{
		"id":             1329259,
		"name":           " Экспозиция  Музея ",
		"ageRestriction": 12,
		"isFree":         false,
		"price":          50,
		"maxPrice":       100,
		"saleLink":       "https://example.test/buy",
		"category":       map[string]any{"sysName": "vystavki"},
		"tags":           []any{map[string]any{"sysName": "istoriya"}, map[string]any{"sysName": "nauka"}},
		"organization":   map[string]any{"name": "Музей"},
		"places": []any{map[string]any{
			"id": 56595, "name": "Музей ВДНХ",
			"address": map[string]any{"street": "пр-кт Мира, 119", "fullAddress": "г Москва,г Москва,пр-кт Мира,119",
				"mapPosition": map[string]any{"type": "Point", "coordinates": []any{37.63, 55.83}}},
			"locale": map[string]any{"name": "Москва"},
		}},
		"seances": []any{
			map[string]any{"start": "2026-09-28T07:00:00Z", "end": "2026-09-28T18:00:00Z"},
			map[string]any{"start": "2026-09-29T16:00:00.000Z", "end": "2026-09-29T17:30:00.000Z"},
			map[string]any{"start": "2026-09-27T07:00:00Z", "end": "2026-09-27T18:00:00Z"},
			map[string]any{"start": "2026-10-13T07:00:00Z", "end": "2026-10-13T18:00:00Z"},
			map[string]any{"start": "broken", "end": "2026-10-01T18:00:00Z"},
		},
	}
	if edit != nil {
		edit(g)
	}
	raw, err := json.Marshal(map[string]any{"data": map[string]any{"general": g}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func place(g map[string]any) map[string]any { return g["places"].([]any)[0].(map[string]any) }

func address(g map[string]any) map[string]any { return place(g)["address"].(map[string]any) }

func mkrfCode(t *testing.T, city domain.City, payload []byte) string {
	t.Helper()
	_, err := MkrfEvent(city, "event:1329259", payload, now)
	var bad *DataError
	if !errors.As(err, &bad) {
		t.Fatalf("error %v is not a data error", err)
	}
	return bad.Code
}

//nolint:cyclop // one source fixture checks all event fields and rejection cases
func TestMkrfEvent(t *testing.T) {
	d, err := MkrfEvent(domain.Moscow, "event:1329259", mkrfPayload(t, nil), now)
	if err != nil {
		t.Fatal(err)
	}
	p := d.Place
	if p.ExternalID != "place:56595" || p.Title != "Музей ВДНХ" || p.Category != "" || p.Tags != nil ||
		string(p.OpeningRules) != "{}" || p.Lat != 55.83 || p.Lon != 37.63 || *p.Address != "пр-кт Мира, 119" {
		t.Fatalf("place %+v", p)
	}
	e := d.Event
	if e.ExternalID != "event:1329259@place:56595" || e.Title != "Экспозиция Музея" ||
		e.NormalizedTitle != "экспозиция музея" ||
		e.Category != "culture" ||
		!slices.Equal(e.Tags, []string{"classical_art", "science_tech"}) ||
		*e.Organizer != "Музей" ||
		*e.AgeMin != 12 {
		t.Fatalf("event %+v", e)
	}
	if len(e.Sessions) != 2 {
		t.Fatalf("sessions %+v", e.Sessions)
	}
	window, show := e.Sessions[0], e.Sessions[1]
	if window.SlotType != "CONTINUOUS_WINDOW" || !window.StartsAt.Equal(time.Date(2026, 9, 28, 7, 0, 0, 0, time.UTC)) ||
		window.Price.Status != "range" || *window.Price.AmountMax != 10000 || *window.BookingURL != "https://example.test/buy" {
		t.Fatalf("window %+v", window)
	}
	if show.SlotType != "FIXED_SESSION" || show.MinDuration != 90*time.Minute {
		t.Fatalf("show %+v", show)
	}
}

func TestMkrfCategories(t *testing.T) {
	for sysName, want := range map[string]struct {
		category string
		tags     []string
	}{
		"ekskursii": {"tourism", []string{"excursions"}},
		"spektakli": {"culture", []string{"performing_arts"}},
		"koncerty":  {"culture", []string{"performing_arts"}},
		"obuchenie": {"culture", []string{"lectures_workshops"}},
		"vstrechi":  {"culture", []string{"lectures_workshops"}},
		"kino":      {"culture", []string{"cinema"}},
		"vystavki":  {"culture", nil},
		"prochie":   {"culture", nil},
	} {
		d, err := MkrfEvent(domain.Moscow, "event:1", mkrfPayload(t, func(g map[string]any) {
			g["category"] = map[string]any{"sysName": sysName}
			delete(g, "tags")
		}), now)
		if err != nil || d.Event.Category != want.category || !slices.Equal(d.Event.Tags, want.tags) {
			t.Errorf("%s: %+v %v", sysName, d.Event, err)
		}
	}
}

func TestMkrfTagInterests(t *testing.T) {
	for tag, want := range map[string]string{
		"sovremennoe-iskusstvo": "contemporary_art", "klassicheskoe-iskusstvo": "classical_art", "istoriya": "classical_art",
		"izobrazitelnoe-iskusstvo": "classical_art", "zhivopis": "classical_art", "skulptura": "classical_art",
		"grafika": "classical_art", "arhitektura": "classical_art", "dekorativno-prikladnoe-iskusstvo": "classical_art",
		"nauka": "science_tech", "estestvennye-nauki": "science_tech", "nauka-i-tehnika": "science_tech",
		"lekcii": "lectures_workshops", "master-klassy": "lectures_workshops", "kinematograf": "cinema", "ekskursii": "excursions",
	} {
		d, err := MkrfEvent(domain.Moscow, "event:1", mkrfPayload(t, func(g map[string]any) {
			g["tags"] = []any{map[string]any{"sysName": tag}, map[string]any{"sysName": "dlya-detey"}}
		}), now)
		if err != nil || !slices.Equal(d.Event.Tags, []string{want}) {
			t.Errorf("%s: %v %v", tag, d.Event.Tags, err)
		}
	}
}

func TestMkrfCoordinatesInEitherOrder(t *testing.T) {
	d, err := MkrfEvent(domain.Moscow, "event:1", mkrfPayload(t, func(g map[string]any) {
		address(g)["mapPosition"] = map[string]any{"coordinates": []any{55.83, 37.63}}
	}), now)
	if err != nil || d.Place.Lat != 55.83 || d.Place.Lon != 37.63 {
		t.Fatalf("lat first: %+v %v", d.Place, err)
	}
	perm := func(coordinates []any) func(g map[string]any) {
		return func(g map[string]any) {
			address(g)["fullAddress"] = "г Пермь,ул Ленина,1"
			place(g)["locale"] = map[string]any{"name": "Пермь"}
			address(g)["mapPosition"] = map[string]any{"coordinates": coordinates}
		}
	}
	for _, coordinates := range [][]any{{56.25, 58.01}, {58.01, 56.25}} {
		d, err := MkrfEvent(domain.Perm, "event:1", mkrfPayload(t, perm(coordinates)), now)
		if err != nil || d.Place.Lat != 58.01 || d.Place.Lon != 56.25 {
			t.Fatalf("perm %v: %+v %v", coordinates, d.Place, err)
		}
	}
	if code := mkrfCode(t, domain.Perm, mkrfPayload(t, perm([]any{37.63, 55.83}))); code != "bad_coordinates" {
		t.Fatalf("moscow point in perm: %s", code)
	}
	if code := mkrfCode(
		t,
		domain.Moscow,
		mkrfPayload(t, func(g map[string]any) { delete(address(g), "mapPosition") }),
	); code != "bad_coordinates" {
		t.Fatalf("no coordinates: %s", code)
	}
}

func TestMkrfPlaceChoice(t *testing.T) {
	d, err := MkrfEvent(domain.Perm, "event:1", mkrfPayload(t, func(g map[string]any) {
		g["places"] = append(g["places"].([]any), map[string]any{
			"id": 7, "name": "Пермская галерея",
			"address": map[string]any{"fullAddress": "г Пермь,Комсомольский пр-кт,4",
				"mapPosition": map[string]any{"coordinates": []any{56.25, 58.01}}},
		})
	}), now)
	if err != nil || d.Place.ExternalID != "place:7" || d.Event.ExternalID != "event:1@place:7" ||
		d.Place.Address != nil {
		t.Fatalf("perm place: %+v %v", d.Place, err)
	}
	if code := mkrfCode(t, domain.Perm, mkrfPayload(t, nil)); code != "missing_place" {
		t.Fatalf("no place in city: %s", code)
	}
	if code := mkrfCode(
		t,
		domain.Moscow,
		mkrfPayload(t, func(g map[string]any) { delete(place(g), "id") }),
	); code != "missing_place" {
		t.Fatalf("place without id: %s", code)
	}
	if code := mkrfCode(
		t,
		domain.Moscow,
		mkrfPayload(t, func(g map[string]any) { place(g)["name"] = " " }),
	); code != "missing_name" {
		t.Fatalf("place without name: %s", code)
	}
	if code := mkrfCode(
		t,
		domain.Moscow,
		mkrfPayload(t, func(g map[string]any) { g["name"] = "" }),
	); code != "missing_name" {
		t.Fatalf("event without name: %s", code)
	}
	if code := mkrfCode(t, domain.Moscow, []byte(`{"data":`)); code != "bad_payload" {
		t.Fatalf("broken json: %s", code)
	}
}

func TestMkrfPrices(t *testing.T) {
	for name, tc := range map[string]struct {
		edit   func(g map[string]any)
		status string
		lo, hi *int64
		label  string
	}{
		"free flag":         {func(g map[string]any) { g["isFree"] = true; delete(g, "price"); delete(g, "maxPrice") }, "free", amount(0), amount(0), ""},
		"free with zeros":   {func(g map[string]any) { g["isFree"] = true; g["price"] = 0; g["maxPrice"] = 0 }, "free", amount(0), amount(0), ""},
		"zero price only":   {func(g map[string]any) { g["price"] = 0; delete(g, "maxPrice") }, "free", amount(0), amount(0), ""},
		"range":             {nil, "range", amount(5000), amount(10000), ""},
		"fixed":             {func(g map[string]any) { g["maxPrice"] = 50 }, "fixed", amount(5000), amount(5000), ""},
		"lower bound only":  {func(g map[string]any) { g["price"] = 600; delete(g, "maxPrice") }, "unknown", nil, nil, "от 600 ₽"},
		"min above max":     {func(g map[string]any) { g["price"] = 200 }, "unknown", nil, nil, ""},
		"negative":          {func(g map[string]any) { g["price"] = -5 }, "unknown", nil, nil, ""},
		"negative max":      {func(g map[string]any) { g["price"] = 0; g["maxPrice"] = -1 }, "unknown", nil, nil, ""},
		"no price not free": {func(g map[string]any) { delete(g, "price"); delete(g, "maxPrice") }, "unknown", nil, nil, ""},
	} {
		d, err := MkrfEvent(domain.Moscow, "event:1", mkrfPayload(t, tc.edit), now)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		p := d.Event.Sessions[0].Price
		label := ""
		if p.TariffLabel != nil {
			label = *p.TariffLabel
		}
		if p.Status != tc.status || !sameAmount(p.AmountMin, tc.lo) || !sameAmount(p.AmountMax, tc.hi) ||
			label != tc.label {
			t.Errorf("%s: %+v label %q", name, p, label)
		}
		if want := map[bool]string{true: "free", false: "ticket"}[tc.status == "free"]; d.Event.Sessions[0].AccessType != want {
			t.Errorf("%s: access %s", name, d.Event.Sessions[0].AccessType)
		}
	}
}

func sameAmount(a, b *int64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func TestMkrfAgeAndOptionalFields(t *testing.T) {
	d, err := MkrfEvent(domain.Moscow, "event:1", mkrfPayload(t, func(g map[string]any) {
		g["ageRestriction"] = -1
		delete(g, "saleLink")
		g["organization"] = map[string]any{}
		g["seances"] = []any{}
	}), now)
	if err != nil || d.Event.AgeMin != nil || d.Event.Organizer != nil || d.Event.Sessions != nil {
		t.Fatalf("event %+v %v", d.Event, err)
	}
}
