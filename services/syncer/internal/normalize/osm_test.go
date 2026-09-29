package normalize

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
)

func node(tags string) []byte {
	return []byte(`{"type":"node","id":1,"lat":58.01,"lon":56.25,"tags":` + tags + `}`)
}

func weeklyOf(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var r map[string]any
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestOSMPlaceMapsTagsInTableOrder(t *testing.T) {
	for _, tc := range []struct {
		tags     string
		category string
		tag      string
	}{
		{`{"name":"M","tourism":"museum"}`, "culture", "classical_art"},
		{`{"name":"G","tourism":"gallery"}`, "culture", "contemporary_art"},
		{`{"name":"A","amenity":"arts_centre"}`, "culture", "contemporary_art"},
		{`{"name":"T","amenity":"theatre"}`, "culture", "performing_arts"},
		{`{"name":"C","amenity":"cinema"}`, "culture", "cinema"},
		{`{"name":"Z","tourism":"zoo"}`, "tourism", "excursions"},
		{`{"name":"P","tourism":"theme_park"}`, "tourism", "excursions"},
		{`{"name":"P","leisure":"park"}`, "walk", "city_walk"},
		{`{"name":"G","leisure":"garden"}`, "walk", "city_walk"},
		{`{"name":"S","leisure":"sports_centre"}`, "sport", ""},
		{`{"name":"S","leisure":"stadium"}`, "sport", ""},
		{`{"name":"C","amenity":"cafe"}`, "gastro", "gastro_coffee"},
		{`{"name":"R","amenity":"restaurant"}`, "gastro", "gastro_coffee"},
		{`{"name":"F","amenity":"fast_food"}`, "gastro", "gastro_coffee"},
		{`{"name":"F","amenity":"food_court"}`, "gastro", "gastro_coffee"},
		{`{"name":"Both","amenity":"cafe","tourism":"museum"}`, "culture", "classical_art"},
	} {
		t.Run(tc.tags, func(t *testing.T) {
			p, err := OSMPlace("node/1", node(tc.tags))
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			if tc.tag != "" {
				want = []string{tc.tag}
			}
			if p.Category != tc.category || !slices.Equal(p.Tags, want) {
				t.Fatalf("category %q tags %v", p.Category, p.Tags)
			}
		})
	}
}

func TestOSMPlaceFields(t *testing.T) {
	way := []byte(
		`{"type":"way","id":7,"center":{"lat":58.04,"lon":56.32},"tags":{"name":"  Кофейня Ёлка ","amenity":"cafe",
		"addr:street":"улица Ленина","addr:housenumber":"5","opening_hours":"Mo-Fr 08:00-20:00"}}`,
	)
	p, err := OSMPlace("way/7", way)
	if err != nil {
		t.Fatal(err)
	}
	if p.ExternalID != "way/7" || p.Title != "Кофейня Ёлка" || p.NormalizedTitle != "кофейня елка" || p.Lat != 58.04 ||
		p.Lon != 56.32 {
		t.Fatalf("place %+v", p)
	}
	if p.Address == nil || *p.Address != "улица Ленина, 5" {
		t.Fatalf("address %v", p.Address)
	}
	rules := weeklyOf(t, p.OpeningRules)
	if rules["source_text"] != "Mo-Fr 08:00-20:00" || rules["schema_version"] != float64(1) {
		t.Fatalf("opening rules %s", p.OpeningRules)
	}

	p, err = OSMPlace("node/1", node(`{"name":"C","amenity":"cafe","addr:street":"Ленина"}`))
	if err != nil || p.Address == nil || *p.Address != "Ленина" {
		t.Fatalf("street only: %v %v", p.Address, err)
	}
	p, err = OSMPlace("node/1", node(`{"name":"C","amenity":"cafe","addr:housenumber":"5"}`))
	if err != nil || p.Address != nil {
		t.Fatalf("number only: %v %v", p.Address, err)
	}
}

func TestOSMPlaceOpeningRules(t *testing.T) {
	park, err := OSMPlace("node/1", node(`{"name":"P","leisure":"park"}`))
	if err != nil {
		t.Fatal(err)
	}
	r := weeklyOf(t, park.OpeningRules)
	if _, hasText := r["source_text"]; hasText || len(r["weekly"].(map[string]any)["sun"].([]any)) != 1 {
		t.Fatalf("park without hours %s", park.OpeningRules)
	}
	for _, tags := range []string{
		`{"name":"P","leisure":"park","opening_hours":"PH off"}`,
		`{"name":"C","amenity":"cafe"}`,
		`{"name":"M","tourism":"museum","opening_hours":"by appointment"}`,
	} {
		p, err := OSMPlace("node/1", node(tags))
		if err != nil || string(p.OpeningRules) != "{}" {
			t.Fatalf("%s: rules %s, err %v", tags, p.OpeningRules, err)
		}
	}
}

func TestOSMPlaceDataErrors(t *testing.T) {
	for payload, code := range map[string]string{
		`{"type":"node","id":1,"lat":58,"lon":56,"tags":{"amenity":"cafe"}}`:              "missing_name",
		`{"type":"node","id":1,"lat":58,"lon":56,"tags":{"name":" ","amenity":"cafe"}}`:   "missing_name",
		`{"type":"way","id":1,"tags":{"name":"C","amenity":"cafe"}}`:                      "bad_coordinates",
		`{"type":"node","id":1,"lat":91,"lon":56,"tags":{"name":"C","amenity":"cafe"}}`:   "bad_coordinates",
		`{"type":"node","id":1,"lat":58,"lon":-181,"tags":{"name":"C","amenity":"cafe"}}`: "bad_coordinates",
		`{"type":"node","id":1,"lat":58,"lon":56,"tags":{"name":"B","amenity":"bar"}}`:    "unmapped_tags",
		`{"type":"node",`: "bad_payload",
	} {
		_, err := OSMPlace("node/1", []byte(payload))
		var de *DataError
		if !errors.As(err, &de) || de.Code != code {
			t.Fatalf("%s: err %v, want %s", payload, err, code)
		}
	}
}
