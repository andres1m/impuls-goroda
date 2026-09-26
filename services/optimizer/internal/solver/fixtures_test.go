package solver

import (
	"math"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

var (
	day    = time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	origin = domain.Coordinate{Longitude: 56.25, Latitude: 58.0}
	source = domain.Provenance{SourceName: "fixture", FetchedAt: day}
)

func at(hour, minute int) time.Time {
	return day.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

// north moves along a meridian, where the great-circle distance equals the arc length exactly.
func north(from domain.Coordinate, meters float64) domain.Coordinate {
	return domain.Coordinate{Longitude: from.Longitude, Latitude: from.Latitude + meters/earthRadiusMeters*180/math.Pi}
}

func problem() Problem {
	return Problem{
		Start:     at(10, 0),
		End:       at(18, 0),
		Origin:    origin,
		Archetype: domain.ArchetypeHistoryHeritage,
		Modes:     []domain.MovementMode{domain.MovementWalk, domain.MovementTransit},
		Currency:  "RUB",
	}
}

func place(id byte, category domain.Category, mask domain.InterestMask, location domain.Coordinate) domain.Candidate {
	return domain.Candidate{
		Place: domain.Place{
			ID: domain.PlaceID{id}, City: "perm", Title: "Place", Category: &category,
			InterestMask: mask, Location: location, DataMode: domain.DataSynthetic, Provenance: source,
		},
		Window: domain.VisitWindow{
			Kind: domain.WindowContinuous, Start: at(9, 0), End: at(20, 0),
			MinDuration: 30 * time.Minute, RecommendedDuration: time.Hour,
		},
		BaseScore: 10,
	}
}

func session(id byte, category domain.Category, location domain.Coordinate, start, end time.Time) domain.Candidate {
	c := place(id, category, 0, location)
	window := domain.VisitWindow{Kind: domain.WindowFixed, Start: start, End: end, MinDuration: end.Sub(start), RecommendedDuration: end.Sub(start)}
	c.Event = &domain.Event{ID: domain.EventID{id}, PlaceID: c.Place.ID, Title: "Event", Category: category, DataMode: domain.DataSynthetic, Provenance: source}
	c.Session = &domain.Session{
		ID: domain.SessionID{id}, EventID: c.Event.ID, Window: window, Access: domain.AccessFree,
		Availability: domain.AvailabilityAvailable, Version: 1, DataMode: domain.DataSynthetic, Provenance: source,
	}
	c.Window = window
	return c
}
