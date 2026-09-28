package usecase

import (
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/solver"
)

type countingTransit struct {
	base  solver.Transit
	calls *atomic.Int64
}

func (c countingTransit) Estimate(from, to domain.Coordinate, departAt time.Time, modes []domain.MovementMode) (domain.TransitEstimate, bool) {
	c.calls.Add(1)
	return c.base.Estimate(from, to, departAt, modes)
}

type departStatusTransit struct {
	base   solver.Transit
	at     time.Time
	status domain.VerificationStatus
}

func (d departStatusTransit) Estimate(from, to domain.Coordinate, departAt time.Time, modes []domain.MovementMode) (domain.TransitEstimate, bool) {
	t, ok := d.base.Estimate(from, to, departAt, modes)
	if departAt.Equal(d.at) {
		t.Verification = d.status
	}
	return t, ok
}

func withLunch(r *domain.OptimizeRequest) {
	r.Constraints.LunchWindow = &domain.LunchWindow{Start: at(13, 0), End: at(14, 30), MinDuration: 30 * time.Minute}
}

func lunchStep(t *testing.T, route domain.Plan) (int, domain.Step) {
	t.Helper()
	i := slices.IndexFunc(route.Steps, func(s domain.Step) bool {
		return slices.ContainsFunc(s.AppliedConstraints, func(c domain.AppliedConstraint) bool { return c.Code == "LUNCH_WINDOW" })
	})
	if i < 0 {
		t.Fatalf("route %s has no lunch", route.Archetype)
	}
	step := route.Steps[i]
	if step.VisitStartAt.Before(at(13, 0)) || step.VisitEndAt.After(at(14, 30)) || step.VisitEndAt.Sub(step.VisitStartAt) != 45*time.Minute {
		t.Fatalf("lunch %s–%s is not 45 minutes inside the window", step.VisitStartAt.Format(time.Kitchen), step.VisitEndAt.Format(time.Kitchen))
	}
	return i, step
}

func TestOptimizeLunchAtVenue(t *testing.T) {
	cafe := place(8, domain.CategoryGastro, north(origin, 150))
	res := optimize(t, append(city(), cafe), estimated(), withLunch)
	if res.Status != domain.ResultReady || len(res.Routes) == 0 {
		t.Fatalf("status %s", res.Status)
	}
	for _, route := range res.Routes {
		_, step := lunchStep(t, route)
		if step.Kind != domain.StepVisit || step.Catalog.PlaceID != cafe.Place.ID {
			t.Fatalf("lunch step %+v is not at the cafe", step)
		}
		if slices.Contains(warningCodes(route.Warnings), "LUNCH_NO_VENUE") {
			t.Fatal("venue lunch warns about a missing venue")
		}
	}
}

func TestOptimizeFreeLunch(t *testing.T) {
	cafe := place(8, domain.CategoryGastro, north(origin, 150))
	for name, change := range map[string]func(*domain.OptimizeRequest){
		"no venue": withLunch,
		"gastro excluded": func(r *domain.OptimizeRequest) {
			withLunch(r)
			r.Constraints.ExcludedCategories = []domain.Category{domain.CategoryGastro}
		},
	} {
		t.Run(name, func(t *testing.T) {
			pool := city()
			if name != "no venue" {
				pool = append(pool, cafe)
			}
			res := optimize(t, pool, estimated(), change)
			if res.Status != domain.ResultReady || len(res.Routes) == 0 {
				t.Fatalf("status %s", res.Status)
			}
			for _, route := range res.Routes {
				i, step := lunchStep(t, route)
				if step.Kind != domain.StepFreeTime || step.Catalog != nil || step.Cost != nil {
					t.Fatalf("free lunch %+v carries a booking or a price", step)
				}
				into := route.Legs[i]
				if *into.DistanceMeters != 0 || into.Cost.Price.Status != domain.PriceFree {
					t.Fatalf("leg into the free lunch %+v moves or costs", into)
				}
				// With the cafe excluded a venue exists, so the warning must not claim there is none.
				if !slices.ContainsFunc(route.Warnings, func(w domain.Warning) bool {
					return w.Code == "LUNCH_NO_VENUE" && w.VisitID != nil && *w.VisitID == step.VisitID &&
						strings.Contains(w.Message, "fits the lunch time and the route's conditions")
				}) {
					t.Fatalf("warnings %v miss LUNCH_NO_VENUE for the pause", warningCodes(route.Warnings))
				}
				if len(route.Geometry) != len(route.Steps)+1 {
					t.Fatalf("geometry has %d points for %d steps with a pause and a destination", len(route.Geometry), len(route.Steps))
				}
			}
		})
	}
}

func TestOptimizeLunchNotReserved(t *testing.T) {
	concert := session(9, domain.CategoryCulture, north(origin, 300), at(12, 30), at(15, 0), 50000)
	res := optimize(t, append(city(), concert), estimated(), func(r *domain.OptimizeRequest) {
		withLunch(r)
		r.Constraints.Obligations = []domain.Obligation{{SessionID: &domain.SessionID{9}, Participation: domain.ParticipationUserReported}}
	})
	if res.Status != domain.ResultReady || len(res.Routes) == 0 || !slices.Contains(warningCodes(res.Warnings), "LUNCH_NOT_RESERVED") {
		t.Fatalf("status %s warnings %v", res.Status, warningCodes(res.Warnings))
	}
	for _, route := range res.Routes {
		for _, step := range route.Steps {
			if step.Kind == domain.StepFreeTime {
				t.Fatal("route reserves a lunch that does not fit")
			}
		}
	}

	var withoutLunchCalls, outsideDayCalls atomic.Int64
	base := statusTransit{base: baseline(), status: domain.VerificationEstimated}
	optimize(t, city(), &fakeProvider{transit: countingTransit{base: base, calls: &withoutLunchCalls}}, nil)
	res = optimize(t, city(), &fakeProvider{transit: countingTransit{base: base, calls: &outsideDayCalls}}, func(r *domain.OptimizeRequest) {
		r.Constraints.LunchWindow = &domain.LunchWindow{Start: at(17, 30), End: at(18, 30), MinDuration: 30 * time.Minute}
	})
	if res.Status != domain.ResultReady || len(res.Routes) == 0 || !slices.Contains(warningCodes(res.Warnings), "LUNCH_NOT_RESERVED") {
		t.Fatalf("outside-day lunch: status %s warnings %v", res.Status, warningCodes(res.Warnings))
	}
	if outsideDayCalls.Load() != withoutLunchCalls.Load() {
		t.Fatalf("outside-day lunch made %d transit estimates, want %d (single search pass)", outsideDayCalls.Load(), withoutLunchCalls.Load())
	}

	overclaimingAfterLunch := &fakeProvider{
		transit: departStatusTransit{base: base, at: at(13, 45), status: domain.VerificationVerified},
	}
	res = optimize(t, []domain.Candidate{place(1, domain.CategoryCulture, north(origin, 300))}, overclaimingAfterLunch, func(r *domain.OptimizeRequest) {
		r.Start, r.End = at(13, 0), at(15, 0)
		withLunch(r)
	})
	if res.Status != domain.ResultReady || len(res.Routes) == 0 || !slices.Contains(warningCodes(res.Warnings), "LUNCH_NOT_RESERVED") {
		t.Fatalf("rejected lunch branches: status %s routes %d warnings %v", res.Status, len(res.Routes), warningCodes(res.Warnings))
	}
}

func TestOptimizeWithoutLunchRequest(t *testing.T) {
	res := optimize(t, append(city(), place(8, domain.CategoryGastro, north(origin, 150))), estimated(), nil)
	if slices.Contains(warningCodes(res.Warnings), "LUNCH_NOT_RESERVED") {
		t.Fatal("lunch warning without a lunch request")
	}
	for _, route := range res.Routes {
		for _, step := range route.Steps {
			if slices.ContainsFunc(step.AppliedConstraints, func(c domain.AppliedConstraint) bool { return c.Code == "LUNCH_WINDOW" }) {
				t.Fatal("lunch reserved without a request")
			}
		}
	}
}

func TestOptimizeLunchAtVenueWithUnknownHoursWarns(t *testing.T) {
	cafe := place(8, domain.CategoryGastro, north(origin, 150))
	cafe.Window.HoursUnknown = true
	res := optimize(t, append(city(), cafe), estimated(), withLunch)
	if res.Status != domain.ResultReady || len(res.Routes) == 0 {
		t.Fatalf("status %s", res.Status)
	}
	for _, route := range res.Routes {
		_, step := lunchStep(t, route)
		if step.Catalog == nil || step.Catalog.PlaceID != cafe.Place.ID {
			t.Fatalf("lunch step %+v is not at the cafe", step)
		}
		if !slices.ContainsFunc(route.Warnings, func(w domain.Warning) bool {
			return w.Code == "OPENING_HOURS_UNKNOWN" && w.Scope == domain.ScopeVisit && w.VisitID != nil && *w.VisitID == step.VisitID
		}) {
			t.Fatalf("warnings %v miss OPENING_HOURS_UNKNOWN for the lunch", warningCodes(route.Warnings))
		}
	}
}
