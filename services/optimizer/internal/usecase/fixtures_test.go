package usecase

import (
	"context"
	"math"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/solver"
)

var (
	day         = time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	source      = domain.Provenance{SourceName: "fixture", FetchedAt: day}
	origin      = domain.Coordinate{Longitude: 56.25, Latitude: 58.0}
	destination = north(origin, 200)
	freshness   = domain.DataFreshness{DataMode: domain.DataSynthetic, CatalogRevision: 7}
)

func at(hour, minute int) time.Time {
	return day.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

func north(from domain.Coordinate, meters float64) domain.Coordinate {
	return domain.Coordinate{Longitude: from.Longitude, Latitude: from.Latitude + meters/6371000*180/math.Pi}
}

func ptr[T any](v T) *T {
	return &v
}

func place(id byte, category domain.Category, location domain.Coordinate) domain.Candidate {
	return domain.Candidate{
		Place: domain.Place{
			ID: domain.PlaceID{id}, City: "perm", Title: "Place", Category: &category,
			Location: location, DataMode: domain.DataSynthetic, Provenance: source,
		},
		Window: domain.VisitWindow{
			Kind: domain.WindowContinuous, Start: at(9, 0), End: at(20, 0),
			MinDuration: 30 * time.Minute, RecommendedDuration: time.Hour,
		},
		BaseScore: 1,
	}
}

func session(id byte, category domain.Category, location domain.Coordinate, start, end time.Time, price int64) domain.Candidate {
	c := place(id, category, location)
	window := domain.VisitWindow{Kind: domain.WindowFixed, Start: start, End: end, MinDuration: end.Sub(start), RecommendedDuration: end.Sub(start)}
	c.Event = &domain.Event{ID: domain.EventID{id}, PlaceID: c.Place.ID, Title: "Event", Category: category, DataMode: domain.DataSynthetic, Provenance: source}
	c.Session = &domain.Session{
		ID: domain.SessionID{id}, EventID: c.Event.ID, Window: window, Access: domain.AccessTicket,
		Availability: domain.AvailabilityAvailable, Version: 3, DataMode: domain.DataSynthetic, Provenance: source,
	}
	c.Window = window
	c.Offers = []domain.PriceOffer{{
		ID: domain.PriceOfferID{id}, SessionID: c.Session.ID, Audience: domain.AudienceGeneral, Provenance: source,
		Price: domain.Price{Status: domain.PriceFixed, Currency: "RUB", LowerMinor: &price, UpperMinor: &price},
	}}
	return c
}

func request() domain.OptimizeRequest {
	return domain.OptimizeRequest{
		City: "perm", Timezone: "Asia/Yekaterinburg", Start: at(10, 0), End: at(18, 0),
		Origin: origin, Destination: &destination,
		Constraints: domain.RouteConstraints{
			MovementModes: []domain.MovementMode{domain.MovementWalk, domain.MovementTransit},
			LoadProfile:   "moderate",
			Budget:        domain.Budget{Mode: domain.BudgetNone},
		},
	}
}

type fakeSource struct {
	candidates []domain.Candidate
	err        error
}

func (f fakeSource) Candidates(context.Context, domain.OptimizeRequest) ([]domain.Candidate, domain.DataFreshness, error) {
	return f.candidates, freshness, f.err
}

// statusTransit is the straight-line estimate relabelled, standing in for a router.
type statusTransit struct {
	base   solver.Transit
	status domain.VerificationStatus
}

func (s statusTransit) Estimate(from, to domain.Coordinate, departAt time.Time, modes []domain.MovementMode) (domain.TransitEstimate, bool) {
	t, ok := s.base.Estimate(from, to, departAt, modes)
	t.Verification = s.status
	return t, ok
}

type fakeProvider struct {
	transit  solver.Transit
	degraded bool
	err      error
	points   []domain.Coordinate
}

func (f *fakeProvider) Transit(_ context.Context, _ string, points []domain.Coordinate, _ []domain.MovementMode) (solver.Transit, bool, error) {
	f.points = points
	return f.transit, f.degraded, f.err
}

func baseline() solver.Transit {
	t, err := solver.NewBaselineTransit(solver.DefaultTransitParams())
	if err != nil {
		panic(err)
	}
	return t
}

func estimated() *fakeProvider {
	return &fakeProvider{transit: statusTransit{base: baseline(), status: domain.VerificationEstimated}}
}

func config() Config {
	return Config{Currency: "RUB", BeamWidth: 8, Parallelism: 2}
}
