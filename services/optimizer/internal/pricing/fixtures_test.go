package pricing

import (
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

var (
	day    = time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	source = domain.Provenance{SourceName: "fixture", FetchedAt: day}
	tariff = domain.Provenance{SourceName: "tariffs", FetchedAt: day}
)

func at(hour, minute int) time.Time {
	return day.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

func rub(amount int64) domain.Money {
	return domain.Money{AmountMinor: amount, Currency: "RUB"}
}

func ptr[T any](v T) *T {
	return &v
}

func fixed(amount int64) domain.Price {
	return domain.Price{Status: domain.PriceFixed, Currency: "RUB", LowerMinor: ptr(amount), UpperMinor: ptr(amount)}
}

func between(lower, upper int64) domain.Price {
	return domain.Price{Status: domain.PriceRange, Currency: "RUB", LowerMinor: ptr(lower), UpperMinor: ptr(upper)}
}

func free() domain.Price {
	return domain.Price{Status: domain.PriceFree, Currency: "RUB", LowerMinor: ptr(int64(0)), UpperMinor: ptr(int64(0))}
}

func unknown() domain.Price {
	return domain.Price{Status: domain.PriceUnknown, Currency: "RUB"}
}

func offer(id byte, price domain.Price, audience domain.Audience, programs ...string) domain.PriceOffer {
	return domain.PriceOffer{ID: domain.PriceOfferID{id}, Price: price, Audience: audience, BenefitPrograms: programs, Provenance: tariff}
}

func museum() domain.Candidate {
	category := domain.CategoryCulture
	return domain.Candidate{
		Place: domain.Place{
			ID: domain.PlaceID{1}, City: "perm", Title: "Museum", Category: &category,
			Location: domain.Coordinate{Longitude: 56.25, Latitude: 58}, DataMode: domain.DataSynthetic, Provenance: source,
		},
		Window: domain.VisitWindow{
			Kind: domain.WindowContinuous, Start: at(10, 0), End: at(18, 0),
			MinDuration: 30 * time.Minute, RecommendedDuration: time.Hour,
		},
		BaseScore: 1,
	}
}

func concert(offers ...domain.PriceOffer) domain.Candidate {
	c := museum()
	c.Window = domain.VisitWindow{
		Kind: domain.WindowFixed, Start: at(15, 0), End: at(16, 30),
		MinDuration: 90 * time.Minute, RecommendedDuration: 90 * time.Minute,
	}
	c.Event = &domain.Event{ID: domain.EventID{2}, PlaceID: c.Place.ID, Title: "Concert", Category: domain.CategoryCulture, DataMode: domain.DataSynthetic, Provenance: source}
	c.Session = &domain.Session{
		ID: domain.SessionID{3}, EventID: c.Event.ID, Window: c.Window, Access: domain.AccessTicket,
		Availability: domain.AvailabilityAvailable, Version: 1, DataMode: domain.DataSynthetic, Provenance: source,
	}
	for _, o := range offers {
		o.SessionID = c.Session.ID
		c.Offers = append(c.Offers, o)
	}
	return c
}

func policy() Policy {
	return Policy{Currency: "RUB", Budget: domain.Budget{Mode: domain.BudgetNone}}
}

func strict(limit int64) domain.Budget {
	return domain.Budget{Mode: domain.BudgetStrict, Limit: ptr(rub(limit))}
}
