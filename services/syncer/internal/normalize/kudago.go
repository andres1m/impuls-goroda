package normalize

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

type kudagoRecord struct {
	Title          string          `json:"title"`
	ShortTitle     string          `json:"short_title"`
	SiteURL        string          `json:"site_url"`
	Categories     []string        `json:"categories"`
	AgeRestriction json.RawMessage `json:"age_restriction"`
	Price          string          `json:"price"`
	IsFree         bool            `json:"is_free"`
	Place          *struct {
		ID       int64  `json:"id"`
		Title    string `json:"title"`
		Address  string `json:"address"`
		IsClosed bool   `json:"is_closed"`
		Coords   struct {
			Lat *float64 `json:"lat"`
			Lon *float64 `json:"lon"`
		} `json:"coords"`
	} `json:"place"`
	Dates []struct {
		Start            int64             `json:"start"`
		End              int64             `json:"end"`
		IsStartless      bool              `json:"is_startless"`
		IsEndless        bool              `json:"is_endless"`
		UsePlaceSchedule bool              `json:"use_place_schedule"`
		Schedules        []json.RawMessage `json:"schedules"`
	} `json:"dates"`
}

// kudagoCategories are matched in order for the category; interests come from every matching row.
var kudagoCategories = []struct {
	code string
	interest
}{
	{"exhibition", interest{categoryCulture, ""}},
	{"theater", interest{categoryCulture, tagPerformingArts}},
	{"concert", interest{categoryCulture, tagPerformingArts}},
	{"education", interest{categoryCulture, tagLecturesWorkshops}},
	{tagCinema, interest{categoryCulture, tagCinema}},
	{"tour", interest{osmKeyTourism, tagExcursions}},
	{"festival", interest{categoryCulture, ""}},
}

// KudaGoEvent turns a KudaGo event into an event at its place. Only dates with a stated start and end
// become sessions: KudaGo gives most dates without an end, and a guessed end would pass for a fact.
//
//nolint:gocognit // source validation and session extraction form one normalization pass
func KudaGoEvent(city domain.City, externalID string, payload []byte, now time.Time) (Draft, error) {
	var rec kudagoRecord
	if err := json.Unmarshal(payload, &rec); err != nil {
		return Draft{}, &DataError{Code: codeBadPayload}
	}
	title := strings.TrimSpace(rec.ShortTitle)
	if title == "" {
		title = strings.TrimSpace(rec.Title)
	}
	if title == "" {
		return Draft{}, &DataError{Code: codeMissingName}
	}
	p := rec.Place
	if p == nil || p.ID == 0 || p.IsClosed {
		return Draft{}, &DataError{Code: codeMissingPlace}
	}
	placeTitle := strings.TrimSpace(p.Title)
	if placeTitle == "" {
		return Draft{}, &DataError{Code: codeMissingName}
	}
	if p.Coords.Lat == nil || p.Coords.Lon == nil {
		return Draft{}, &DataError{Code: codeBadCoordinates}
	}
	lat, lon, ok := cityPoint(city, *p.Coords.Lat, *p.Coords.Lon)
	if !ok {
		return Draft{}, &DataError{Code: codeBadCoordinates}
	}
	category, tags := kudagoCategory(rec.Categories)
	if category == "" {
		return Draft{}, &DataError{Code: "unmapped_category"}
	}

	place := PlaceDraft{
		ExternalID: "place:" + strconv.FormatInt(
			p.ID,
			10,
		),
		Title:           placeTitle,
		NormalizedTitle: NormalizedTitle(placeTitle),
		Lat:             lat,
		Lon:             lon,
		Address:         optional(strings.TrimSpace(p.Address)),
		OpeningRules:    unknownHours,
	}
	event := &EventDraft{
		ExternalID: externalID + "@" + place.ExternalID, Title: title, NormalizedTitle: NormalizedTitle(title),
		Category: category, Tags: tags, AgeMin: kudagoAge(rec.AgeRestriction),
	}
	price := kudagoPrice(rec.IsFree, rec.Price)
	booking := optional(strings.TrimSpace(rec.SiteURL))
	for _, d := range rec.Dates {
		if len(d.Schedules) > 0 || d.UsePlaceSchedule || d.IsStartless || d.IsEndless {
			continue
		}
		start, end := time.Unix(d.Start, 0), time.Unix(d.End, 0)
		if inHorizon(start, end, now) {
			event.Sessions = addSession(event.Sessions, newSession(start, end, price, booking))
		}
	}
	return Draft{Place: place, Event: event}, nil
}

func kudagoCategory(categories []string) (selectedCategory string, selectedTags []string) {
	var category string
	var tags []string
	for _, row := range kudagoCategories {
		for _, c := range categories {
			if c != row.code {
				continue
			}
			if category == "" {
				category = row.category
			}
			if row.tag != "" {
				tags = append(tags, row.tag)
			}
		}
	}
	return category, uniqueSorted(tags)
}

// kudagoAge reads "18+" or a bare number; JSON null would otherwise decode as age 0.
func kudagoAge(raw json.RawMessage) *int16 {
	var number int16
	if err := json.Unmarshal(raw, &number); err == nil && string(raw) != "null" && number >= 0 {
		return &number
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return nil
	}
	parsed, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(text), "+"), 10, 16)
	if err != nil || parsed < 0 {
		return nil
	}
	age := int16(parsed)
	return &age
}

var (
	priceNumber    = `(\d+(?:[ \x{00a0}]\d{3})*)`
	priceRubles    = `\s*(?:рублей|рубля|рубль|руб\.?)`
	fixedPriceText = regexp.MustCompile(`(?i)^` + priceNumber + priceRubles + `$`)
	rangePriceText = regexp.MustCompile(`(?i)^от\s+` + priceNumber + `\s+до\s+` + priceNumber + priceRubles + `$`)
)

// kudagoPrice reads the free-text price. Anything but an exact sum or an exact range keeps its text and
// stays unknown: "от 990 рублей" has no upper bound to check a budget against.
func kudagoPrice(isFree bool, text string) PriceDraft {
	if isFree {
		return freePrice()
	}
	text = strings.TrimSpace(text)
	if m := fixedPriceText.FindStringSubmatch(text); m != nil {
		if v, ok := rubles(m[1]); ok {
			return fixedPrice(v)
		}
	}
	if m := rangePriceText.FindStringSubmatch(text); m != nil {
		lo, okLo := rubles(m[1])
		hi, okHi := rubles(m[2])
		if okLo && okHi && lo <= hi {
			return rangePrice(lo, hi)
		}
	}
	return unknownPrice(text)
}

func rubles(digits string) (int64, bool) {
	v, err := strconv.ParseInt(strings.NewReplacer(" ", "", " ", "").Replace(digits), 10, 64)
	if err != nil {
		return 0, false
	}
	_, fits := kopecks(v)
	return v, fits
}
