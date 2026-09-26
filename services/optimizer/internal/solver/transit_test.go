package solver

import (
	"math"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

var (
	walkOnly    = []domain.MovementMode{domain.MovementWalk}
	transitOnly = []domain.MovementMode{domain.MovementTransit}
	walkTransit = []domain.MovementMode{domain.MovementWalk, domain.MovementTransit}
)

func baseline(t *testing.T, p TransitParams) *BaselineTransit {
	t.Helper()
	transit, err := NewBaselineTransit(p)
	if err != nil {
		t.Fatal(err)
	}
	return transit
}

func TestDistanceAlongMeridian(t *testing.T) {
	if d := distanceMeters(origin, north(origin, 1000)); math.Abs(d-1000) > 1e-6 {
		t.Fatalf("distance = %f, want 1000", d)
	}
	if d := distanceMeters(origin, origin); d != 0 {
		t.Fatalf("distance to itself = %f", d)
	}
}

func TestBaselineTransit(t *testing.T) {
	transit := baseline(t, DefaultTransitParams())
	cases := []struct {
		name     string
		meters   float64
		modes    []domain.MovementMode
		mode     domain.MovementMode
		duration time.Duration
	}{
		{"short walk", 1000, walkTransit, domain.MovementWalk, 1000 * time.Second},
		{"just below threshold", 1799, walkTransit, domain.MovementWalk, 1799 * time.Second},
		{"just above threshold", 1801, walkTransit, domain.MovementTransit, 1134 * time.Second},
		{"long transit", 3000, walkTransit, domain.MovementTransit, 1410 * time.Second},
		{"walk only never switches", 5000, walkOnly, domain.MovementWalk, 5000 * time.Second},
		{"transit only on short distance", 100, transitOnly, domain.MovementTransit, 743 * time.Second},
	}
	for _, tc := range cases {
		got, ok := transit.Estimate(origin, north(origin, tc.meters), at(10, 0), tc.modes)
		if !ok || got.Mode != tc.mode || got.Duration != tc.duration || got.Verification != domain.VerificationEstimated {
			t.Fatalf("%s: got %+v ok=%v", tc.name, got, ok)
		}
		if math.Abs(got.DistanceMeters-tc.meters) > 1e-6 {
			t.Fatalf("%s: distance %f", tc.name, got.DistanceMeters)
		}
	}
}

func TestBaselineTransitWithoutUsableMode(t *testing.T) {
	transit := baseline(t, DefaultTransitParams())
	if _, ok := transit.Estimate(origin, north(origin, 100), at(10, 0), []domain.MovementMode{domain.MovementCar}); ok {
		t.Fatal("car produced a transit estimate")
	}
}

func TestBaselineTransitUsesGivenParams(t *testing.T) {
	p := DefaultTransitParams()
	p.WalkMetersPerMinute = 50
	got, ok := baseline(t, p).Estimate(origin, north(origin, 1000), at(10, 0), walkOnly)
	if !ok || got.Duration != 25*time.Minute {
		t.Fatalf("got %+v ok=%v, want 25m", got, ok)
	}
}

func TestNewBaselineTransitRejectsInvalidParams(t *testing.T) {
	p := DefaultTransitParams()
	p.WalkMetersPerMinute = 0
	if _, err := NewBaselineTransit(p); err == nil {
		t.Fatal("zero walking speed accepted")
	}
}
