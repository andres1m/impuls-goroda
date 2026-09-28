package normalize

import (
	"encoding/json"
	"strings"
)

// PlaceDraft is a place as a source describes it, in catalog terms.
type PlaceDraft struct {
	ExternalID      string
	Title           string
	NormalizedTitle string
	Category        string
	// Codes of the interest tags the place carries.
	Tags         []string
	Lat, Lon     float64
	Address      *string
	OpeningRules json.RawMessage
}

// DataError marks a record whose content cannot become a catalog row; retrying will not help.
type DataError struct{ Code string }

func (e *DataError) Error() string { return "invalid source data: " + e.Code }

type osmMapping struct {
	key, value string
	category   string
	tag        string
}

// osmMappings are matched in order: an element with several known tags takes the first.
var osmMappings = []osmMapping{
	{"tourism", "museum", "culture", "classical_art"},
	{"tourism", "gallery", "culture", "contemporary_art"},
	{"amenity", "arts_centre", "culture", "contemporary_art"},
	{"amenity", "theatre", "culture", "performing_arts"},
	{"amenity", "cinema", "culture", "cinema"},
	{"tourism", "zoo", "tourism", "excursions"},
	{"tourism", "theme_park", "tourism", "excursions"},
	{"leisure", "park", "walk", "city_walk"},
	{"leisure", "garden", "walk", "city_walk"},
	{"leisure", "sports_centre", "sport", ""},
	{"leisure", "stadium", "sport", ""},
	{"amenity", "cafe", "gastro", "gastro_coffee"},
	{"amenity", "restaurant", "gastro", "gastro_coffee"},
	{"amenity", "fast_food", "gastro", "gastro_coffee"},
	{"amenity", "food_court", "gastro", "gastro_coffee"},
}

type osmPoint struct {
	Lat *float64 `json:"lat"`
	Lon *float64 `json:"lon"`
}

type osmElement struct {
	osmPoint
	Center *osmPoint         `json:"center"`
	Tags   map[string]string `json:"tags"`
}

var unknownHours = json.RawMessage(`{}`)

// OSMPlace turns an Overpass element into a place.
func OSMPlace(externalID string, payload []byte) (PlaceDraft, error) {
	var el osmElement
	if err := json.Unmarshal(payload, &el); err != nil {
		return PlaceDraft{}, &DataError{Code: "bad_payload"}
	}
	title := strings.TrimSpace(el.Tags["name"])
	if title == "" {
		return PlaceDraft{}, &DataError{Code: "missing_name"}
	}
	point := el.osmPoint
	if el.Center != nil {
		point = *el.Center
	}
	if point.Lat == nil || point.Lon == nil || *point.Lat < -90 || *point.Lat > 90 || *point.Lon < -180 || *point.Lon > 180 {
		return PlaceDraft{}, &DataError{Code: "bad_coordinates"}
	}
	i := mappingIndex(el.Tags)
	if i < 0 {
		return PlaceDraft{}, &DataError{Code: "unmapped_tags"}
	}
	m := osmMappings[i]
	p := PlaceDraft{
		ExternalID: externalID, Title: title, NormalizedTitle: NormalizedTitle(title), Category: m.category,
		Lat: *point.Lat, Lon: *point.Lon, Address: osmAddress(el.Tags), OpeningRules: osmOpeningRules(el.Tags, m.category),
	}
	if m.tag != "" {
		p.Tags = []string{m.tag}
	}
	return p, nil
}

func mappingIndex(tags map[string]string) int {
	for i, m := range osmMappings {
		if tags[m.key] == m.value {
			return i
		}
	}
	return -1
}

func osmAddress(tags map[string]string) *string {
	street := strings.TrimSpace(tags["addr:street"])
	if street == "" {
		return nil
	}
	if number := strings.TrimSpace(tags["addr:housenumber"]); number != "" {
		street += ", " + number
	}
	return &street
}

// osmOpeningRules keeps hours the format can hold. Parks and gardens that state no hours at all are
// taken as always open; that is an assumption, so no source text claims otherwise.
func osmOpeningRules(tags map[string]string, category string) json.RawMessage {
	value, stated := tags["opening_hours"]
	if !stated {
		if category != "walk" {
			return unknownHours
		}
		value = "24/7"
	}
	rules, ok := ParseOpeningHours(value)
	if !ok {
		return unknownHours
	}
	if !stated {
		rules.SourceText = ""
	}
	raw, err := rules.MarshalJSON()
	if err != nil {
		return unknownHours
	}
	return raw
}
