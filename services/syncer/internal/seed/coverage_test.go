package seed

import (
	"math/bits"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

type box struct{ minLat, maxLat, minLon, maxLon float64 }

// Coarse city envelopes: a place outside means swapped latitude and longitude or a typo.
var cityBoxes = map[domain.City]box{
	domain.Moscow: {55.1, 56.1, 36.8, 38.0},
	domain.Perm:   {57.8, 58.2, 55.8, 56.7},
}

func TestEmbeddedDatasetsCoverage(t *testing.T) {
	for city, envelope := range cityBoxes {
		t.Run(string(city), func(t *testing.T) {
			ds, err := Load(city)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := Expand(ds, testReference(), "2026-10-01", testNow)
			if err != nil {
				t.Fatal(err)
			}
			checkCoverage(t, ds, rows, envelope)
		})
	}
}

func checkCoverage(t *testing.T, ds Dataset, rows Rows, envelope box) {
	require := func(what string, ok bool) {
		t.Helper()
		if !ok {
			t.Errorf("dataset lacks %s", what)
		}
	}

	for _, p := range ds.Places {
		if p.Lat < envelope.minLat || p.Lat > envelope.maxLat || p.Lon < envelope.minLon || p.Lon > envelope.maxLon {
			t.Errorf("place %q at %f,%f is outside the city", p.Key, p.Lat, p.Lon)
		}
	}

	categories := make(map[string]bool)
	var mask int64
	for _, p := range rows.Places {
		if p.Category != nil {
			categories[*p.Category] = true
		}
		mask |= p.TagMask
	}
	eventCategory := make(map[uuid.UUID]string)
	eventsPerPlace := make(map[uuid.UUID]int)
	for _, e := range rows.Events {
		categories[e.Category] = true
		mask |= e.TagMask
		eventCategory[e.ID] = e.Category
		eventsPerPlace[e.PlaceID]++
	}
	require("all six categories", len(categories) == 6)
	require("eight interest bits", bits.OnesCount64(uint64(mask)) >= 8)

	require("a public space with opening hours", slices.ContainsFunc(ds.Places, func(p Place) bool {
		return p.Kind == "public_space" && p.OpeningRules != nil && p.OpeningRules.hasOpenHours()
	}))
	require("two events at one place", slices.ContainsFunc(rows.Places, func(p PlaceRow) bool { return eventsPerPlace[p.ID] >= 2 }))
	require("an adults-only event", slices.ContainsFunc(rows.Events, func(e EventRow) bool { return e.AgeMin != nil && *e.AgeMin >= 18 }))

	var specs []Session
	for _, e := range ds.Events {
		specs = append(specs, e.Sessions...)
	}
	require("a window closing at 18:00 with a one-hour minimum", slices.ContainsFunc(specs, func(s Session) bool {
		return s.Slot == "window" && s.End == "18:00" && s.MinDuration == time.Hour
	}))
	require("a fixed 17:00 session without late entry", slices.ContainsFunc(specs, func(s Session) bool {
		return s.Slot == "fixed" && s.Start == "17:00" && s.LateEntry != nil && !*s.LateEntry
	}))
	require("late entry with a last entry time", slices.ContainsFunc(specs, func(s Session) bool {
		return s.LateEntry != nil && *s.LateEntry && s.LastEntry != ""
	}))
	require("an arrival buffer", slices.ContainsFunc(specs, func(s Session) bool { return s.Buffer > 0 }))

	require("overlapping fixed sessions of different events", slices.ContainsFunc(rows.Sessions, func(a SessionRow) bool {
		return slices.ContainsFunc(rows.Sessions, func(b SessionRow) bool {
			return a.SlotType == "FIXED_SESSION" && b.SlotType == "FIXED_SESSION" && a.EventID != b.EventID &&
				a.StartsAt.Before(b.EndsAt) && b.StartsAt.Before(a.EndsAt)
		})
	}))
	cancelled := make(map[uuid.UUID]bool)
	running := make(map[uuid.UUID]bool)
	availability := make(map[string]bool)
	for _, s := range rows.Sessions {
		availability[s.Availability] = true
		if s.Availability == "cancelled" {
			cancelled[s.EventID] = true
		} else {
			running[s.EventID] = true
		}
	}
	require("a cancelled session next to running ones", slices.ContainsFunc(rows.Events, func(e EventRow) bool {
		return cancelled[e.ID] && running[e.ID]
	}))
	require("a sold-out session", availability["sold_out"])
	require("a session with unknown availability", availability["unknown"])
	require("an open volunteer shift with a deadline", slices.ContainsFunc(rows.Sessions, func(s SessionRow) bool {
		return eventCategory[s.EventID] == "volunteer" && s.Availability == "registration_required" && s.RegistrationDeadline != nil
	}))
	require("a volunteer shift with closed registration", slices.ContainsFunc(rows.Sessions, func(s SessionRow) bool {
		return eventCategory[s.EventID] == "volunteer" && s.Availability == "sold_out"
	}))

	statuses := make(map[string]bool)
	audiences := make(map[uuid.UUID][]string)
	for _, p := range rows.Prices {
		statuses[p.Status] = true
		audiences[p.SessionID] = append(audiences[p.SessionID], p.Audience)
	}
	require("fixed, range and unknown prices", statuses["fixed"] && statuses["range"] && statuses["unknown"])
	require("a Pushkin card offer", slices.ContainsFunc(rows.Prices, func(p PriceRow) bool {
		return slices.Contains(p.BenefitPrograms, "pushkin_card")
	}))
	require("a session with only a child tariff", slices.ContainsFunc(rows.Sessions, func(s SessionRow) bool {
		return slices.Equal(audiences[s.ID], []string{"child"})
	}))
}
