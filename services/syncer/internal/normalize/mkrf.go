package normalize

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

type mkrfRecord struct {
	Data struct {
		General struct {
			Name           string     `json:"name"`
			AgeRestriction *int16     `json:"ageRestriction"`
			IsFree         bool       `json:"isFree"`
			Price          *int64     `json:"price"`
			MaxPrice       *int64     `json:"maxPrice"`
			SaleLink       string     `json:"saleLink"`
			Category       mkrfSysName   `json:"category"`
			Tags           []mkrfSysName `json:"tags"`
			Organization   struct {
				Name string `json:"name"`
			} `json:"organization"`
			Places  []mkrfPlace `json:"places"`
			Seances []struct {
				Start string `json:"start"`
				End   string `json:"end"`
			} `json:"seances"`
		} `json:"general"`
	} `json:"data"`
}

type mkrfSysName struct {
	SysName string `json:"sysName"`
}

type mkrfPlace struct {
	ID      *int64 `json:"id"`
	Name    string `json:"name"`
	Address struct {
		Street      string `json:"street"`
		FullAddress string `json:"fullAddress"`
		MapPosition struct {
			Coordinates []float64 `json:"coordinates"`
		} `json:"mapPosition"`
	} `json:"address"`
	Locale struct {
		Name string `json:"name"`
	} `json:"locale"`
}

func (p mkrfPlace) in(city domain.City) bool {
	name := cityNames[city]
	return name != "" && (strings.Contains(p.Address.FullAddress, "г "+name) || p.Locale.Name == name)
}

type interest struct{ category, tag string }

var mkrfCategories = map[string]interest{
	"ekskursii": {"tourism", "excursions"},
	"spektakli": {"culture", "performing_arts"},
	"koncerty":  {"culture", "performing_arts"},
	"obuchenie": {"culture", "lectures_workshops"},
	"vstrechi":  {"culture", "lectures_workshops"},
	"kino":      {"culture", "cinema"},
}

var mkrfTagInterests = map[string]string{
	"sovremennoe-iskusstvo":            "contemporary_art",
	"klassicheskoe-iskusstvo":          "classical_art",
	"istoriya":                         "classical_art",
	"izobrazitelnoe-iskusstvo":         "classical_art",
	"zhivopis":                         "classical_art",
	"skulptura":                        "classical_art",
	"grafika":                          "classical_art",
	"arhitektura":                      "classical_art",
	"dekorativno-prikladnoe-iskusstvo": "classical_art",
	"nauka":                            "science_tech",
	"estestvennye-nauki":               "science_tech",
	"nauka-i-tehnika":                  "science_tech",
	"lekcii":                           "lectures_workshops",
	"master-klassy":                    "lectures_workshops",
	"kinematograf":                     "cinema",
	"ekskursii":                        "excursions",
}

// MkrfEvent turns a record of the Ministry of Culture events dataset into an event at its place in the city.
// The whole dataset is about culture, so an unlisted category is culture.
func MkrfEvent(city domain.City, externalID string, payload []byte, now time.Time) (Draft, error) {
	var rec mkrfRecord
	if err := json.Unmarshal(payload, &rec); err != nil {
		return Draft{}, &DataError{Code: "bad_payload"}
	}
	g := rec.Data.General
	title := strings.Join(strings.Fields(g.Name), " ")
	if title == "" {
		return Draft{}, &DataError{Code: "missing_name"}
	}
	place, err := mkrfPlaceDraft(city, g.Places)
	if err != nil {
		return Draft{}, err
	}

	kind, listed := mkrfCategories[g.Category.SysName]
	if !listed {
		kind = interest{category: "culture"}
	}
	var tags []string
	if kind.tag != "" {
		tags = append(tags, kind.tag)
	}
	for _, t := range g.Tags {
		if tag, ok := mkrfTagInterests[t.SysName]; ok {
			tags = append(tags, tag)
		}
	}

	event := &EventDraft{
		ExternalID:      externalID + "@" + place.ExternalID,
		Title:           title,
		NormalizedTitle: NormalizedTitle(title),
		Category:        kind.category,
		Tags:            uniqueSorted(tags),
		Organizer:       optional(strings.TrimSpace(g.Organization.Name)),
	}
	if g.AgeRestriction != nil && *g.AgeRestriction >= 0 {
		event.AgeMin = g.AgeRestriction
	}
	price := mkrfPrice(g.IsFree, g.Price, g.MaxPrice)
	booking := optional(strings.TrimSpace(g.SaleLink))
	for _, s := range g.Seances {
		start, errStart := time.Parse(time.RFC3339, s.Start)
		end, errEnd := time.Parse(time.RFC3339, s.End)
		if errStart != nil || errEnd != nil || !inHorizon(start, end, now) {
			continue
		}
		event.Sessions = addSession(event.Sessions, newSession(start, end, price, booking))
	}
	return Draft{Place: place, Event: event}, nil
}

func mkrfPlaceDraft(city domain.City, places []mkrfPlace) (PlaceDraft, error) {
	for _, p := range places {
		if !p.in(city) {
			continue
		}
		if p.ID == nil {
			return PlaceDraft{}, &DataError{Code: "missing_place"}
		}
		title := strings.TrimSpace(p.Name)
		if title == "" {
			return PlaceDraft{}, &DataError{Code: "missing_name"}
		}
		coordinates := p.Address.MapPosition.Coordinates
		if len(coordinates) != 2 {
			return PlaceDraft{}, &DataError{Code: "bad_coordinates"}
		}
		lat, lon, ok := cityPoint(city, coordinates[0], coordinates[1])
		if !ok {
			return PlaceDraft{}, &DataError{Code: "bad_coordinates"}
		}
		return PlaceDraft{
			ExternalID: "place:" + strconv.FormatInt(*p.ID, 10), Title: title, NormalizedTitle: NormalizedTitle(title),
			Lat: lat, Lon: lon, Address: optional(strings.TrimSpace(p.Address.Street)), OpeningRules: unknownHours,
		}, nil
	}
	return PlaceDraft{}, &DataError{Code: "missing_place"}
}

// mkrfPrice keeps a price only when its upper bound is known: budgets are checked against it.
func mkrfPrice(isFree bool, price, maxPrice *int64) PriceDraft {
	switch {
	case (price != nil && *price < 0) || (maxPrice != nil && *maxPrice < 0):
		return unknownPrice("")
	case isFree, price != nil && *price == 0 && (maxPrice == nil || *maxPrice == 0):
		return freePrice()
	case price == nil:
		return unknownPrice("")
	case maxPrice == nil:
		return unknownPrice(fmt.Sprintf("от %d ₽", *price))
	case *price < *maxPrice:
		return rangePrice(*price, *maxPrice)
	case *price == *maxPrice:
		return fixedPrice(*price)
	}
	return unknownPrice("")
}
