package validation

import (
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

var (
	day          = time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	source       = domain.Provenance{SourceName: "fixture", FetchedAt: day}
	origin       = domain.Coordinate{Longitude: 56.2294, Latitude: 58.0105}
	destination  = domain.Coordinate{Longitude: 56.2350, Latitude: 58.0120}
	museumVisit  = domain.VisitID{1}
	concertVisit = domain.VisitID{2}
)

func at(hour, minute int) time.Time {
	return day.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

func ptr[T any](v T) *T {
	return &v
}

func rub(amount int64) domain.Money {
	return domain.Money{AmountMinor: amount, Currency: "RUB"}
}

func fixed(amount int64) domain.Price {
	return domain.Price{Status: domain.PriceFixed, Currency: "RUB", LowerMinor: ptr(amount), UpperMinor: ptr(amount)}
}

func free() domain.Price {
	return domain.Price{Status: domain.PriceFree, Currency: "RUB", LowerMinor: ptr(int64(0)), UpperMinor: ptr(int64(0))}
}

func museum() domain.Candidate {
	category := domain.CategoryCulture
	return domain.Candidate{
		Place: domain.Place{
			ID: domain.PlaceID{1}, City: "perm", Title: "Museum", Category: &category,
			Location: domain.Coordinate{Longitude: 56.2310, Latitude: 58.0110}, DataMode: domain.DataSynthetic, Provenance: source,
		},
		Window: domain.VisitWindow{
			Kind: domain.WindowContinuous, Start: at(9, 0), End: at(20, 0),
			MinDuration: 30 * time.Minute, RecommendedDuration: time.Hour,
		},
		BaseScore: 1,
	}
}

func concert() domain.Candidate {
	category := domain.CategoryCulture
	window := domain.VisitWindow{
		Kind: domain.WindowFixed, Start: at(13, 0), End: at(14, 30),
		MinDuration: 90 * time.Minute, RecommendedDuration: 90 * time.Minute, ArrivalBuffer: 10 * time.Minute,
	}
	c := domain.Candidate{
		Place: domain.Place{
			ID: domain.PlaceID{2}, City: "perm", Title: "Hall", Category: &category,
			Location: domain.Coordinate{Longitude: 56.2330, Latitude: 58.0115}, DataMode: domain.DataSynthetic, Provenance: source,
		},
		Event: &domain.Event{ID: domain.EventID{2}, PlaceID: domain.PlaceID{2}, Title: "Concert", Category: domain.CategoryCulture, DataMode: domain.DataSynthetic, Provenance: source},
		Session: &domain.Session{
			ID: domain.SessionID{2}, EventID: domain.EventID{2}, Window: window, Access: domain.AccessTicket,
			Availability: domain.AvailabilityAvailable, Version: 1, DataMode: domain.DataSynthetic, Provenance: source,
		},
		Window:    window,
		BaseScore: 1,
	}
	c.Offers = []domain.PriceOffer{{
		ID: domain.PriceOfferID{5}, SessionID: c.Session.ID, Price: fixed(50000), Audience: domain.AudienceGeneral,
		BenefitPrograms: []string{domain.ProgramPushkinCard}, Provenance: source,
	}}
	return c
}

func snapshot(c domain.Candidate) *domain.CatalogSnapshot {
	s := &domain.CatalogSnapshot{
		PlaceID: c.Place.ID, Title: c.Place.Title, Category: c.Category(), InterestMask: c.InterestMask(),
		Availability: domain.AvailabilityAvailable, DataMode: c.DataMode(), Provenance: c.Place.Provenance,
	}
	if c.Session != nil {
		s.EventID, s.SessionID = &c.Event.ID, &c.Session.ID
		s.Availability = c.Session.Availability
		s.SessionStart, s.SessionEnd = &c.Window.Start, &c.Window.End
		s.SessionVersion = "1"
	}
	return s
}

func walk(position int, from, to *domain.VisitID, departure, arrival time.Time) domain.Leg {
	leg := domain.Leg{
		Position: position, From: domain.EndpointVisit, To: domain.EndpointVisit, FromVisitID: from, ToVisitID: to,
		DepartureAt: departure, ArrivalAt: arrival, Mode: domain.MovementWalk, Verification: domain.VerificationEstimated,
		Evidence: domain.LegEvidence{Provider: "osrm", Method: "osm_foot_network", ObservedAt: day, Mode: "walk", Limitations: []string{"time_is_modelled"}},
		Cost:     domain.CostSnapshot{Price: domain.Price{Status: domain.PriceFree, Currency: "RUB", LowerMinor: ptr(int64(0)), UpperMinor: ptr(int64(0))}, Provenance: source},
	}
	if from == nil {
		leg.From = domain.EndpointOrigin
	}
	if to == nil {
		leg.To = domain.EndpointDestination
	}
	return leg
}

// validPlan is a museum visit with an unknown price followed by a ticketed concert, walked
// between, ending at a destination.
func validPlan() (domain.Plan, Input) {
	m, c := museum(), concert()
	plan := domain.Plan{
		Archetype: domain.ArchetypeHistoryHeritage, Start: at(10, 0), End: at(18, 0),
		Origin: origin, Destination: &destination, CatalogRevision: 1, Result: domain.ResultReady,
		Cost: domain.CostSummary{
			KnownPersonal: rub(50000), KnownTransport: rub(0), ProgramAmount: rub(0),
			UnknownComponents: []domain.UnknownCostComponent{{Code: "PRICE_UNKNOWN", Message: "Ticket price is unknown"}},
			BudgetConclusion:  domain.BudgetNotApplicable,
		},
		Steps: []domain.Step{
			{
				VisitID: museumVisit, Kind: domain.StepVisit, Position: 1,
				ArrivalAt: at(10, 10), VisitStartAt: at(10, 10), VisitEndAt: at(11, 10), DepartureAt: at(11, 10), MinDuration: 30 * time.Minute,
				Participation: domain.Participation{Status: domain.ParticipationNotRequired, Evidence: domain.EvidenceNone},
				Catalog:       snapshot(m),
				Cost: &domain.CostSnapshot{
					Price:             domain.Price{Status: domain.PriceUnknown, Currency: "RUB"},
					UnknownComponents: []domain.UnknownCostComponent{{Code: "PRICE_UNKNOWN", Message: "Ticket price is unknown"}},
					Provenance:        source,
				},
			},
			{
				VisitID: concertVisit, Kind: domain.StepVisit, Position: 2,
				ArrivalAt: at(11, 25), VisitStartAt: at(13, 0), VisitEndAt: at(14, 30), DepartureAt: at(14, 30), MinDuration: 90 * time.Minute,
				Participation: domain.Participation{Status: domain.ParticipationActionRequired, Evidence: domain.EvidenceNone},
				Catalog:       snapshot(c),
				Cost: &domain.CostSnapshot{
					PriceOfferID: &c.Offers[0].ID, Audience: domain.AudienceGeneral, Price: fixed(50000),
					PersonalAmount: ptr(rub(50000)), Provenance: source,
				},
			},
		},
		Legs: []domain.Leg{
			walk(1, nil, &museumVisit, at(10, 0), at(10, 10)),
			walk(2, &museumVisit, &concertVisit, at(11, 10), at(11, 25)),
			walk(3, &concertVisit, nil, at(14, 30), at(14, 40)),
		},
	}
	in := Input{
		Constraints: domain.RouteConstraints{
			MovementModes: []domain.MovementMode{domain.MovementWalk, domain.MovementTransit},
			LoadProfile:   "moderate",
			Budget:        domain.Budget{Mode: domain.BudgetNone},
		},
		Currency:   "RUB",
		Candidates: map[domain.VisitID]domain.Candidate{museumVisit: m, concertVisit: c},
	}
	return plan, in
}
