package routing

import (
	"context"
	"math"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// The Kama splits Perm, so a walk across it must follow a bridge rather than the straight line.
func TestRouterAcrossTheKamaIntegration(t *testing.T) {
	footURL, carURL := os.Getenv("OPTIMIZER_TEST_OSRM_FOOT_URL"), os.Getenv("OPTIMIZER_TEST_OSRM_CAR_URL")
	if footURL == "" || carURL == "" {
		t.Skip("OPTIMIZER_TEST_OSRM_FOOT_URL and OPTIMIZER_TEST_OSRM_CAR_URL are not set")
	}
	cfg := config()
	city := cityConfig()
	city.Foot, city.Car = footURL, carURL
	cfg.Cities = map[string]CityConfig{"perm": city}
	r, err := NewRouter(cfg, NewClient(&http.Client{}))
	if err != nil {
		t.Fatal(err)
	}
	centre := domain.Coordinate{Longitude: 56.2294, Latitude: 58.0105}
	rightBank := domain.Coordinate{Longitude: 56.3100, Latitude: 58.0500}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rule, degraded, err := r.Transit(ctx, "perm", []domain.Coordinate{centre, rightBank}, modes(walk, transit, car))
	if err != nil || degraded {
		t.Fatalf("degraded=%v err=%v", degraded, err)
	}

	now := time.Now()
	walking, ok := rule.Estimate(centre, rightBank, now, modes(walk))
	if !ok || walking.Mode != walk || walking.Verification != domain.VerificationEstimated {
		t.Fatalf("walk %+v %v", walking, ok)
	}
	if straight := straightMeters(centre, rightBank); walking.DistanceMeters < straight*1.1 {
		t.Fatalf("walk of %.0f m is barely longer than the straight %.0f m", walking.DistanceMeters, straight)
	}
	if observed := walking.Evidence.ObservedAt; observed.IsZero() || observed.After(now) {
		t.Fatalf("graph date %s", observed)
	}
	driving, ok := rule.Estimate(centre, rightBank, now, modes(car))
	if !ok || driving.Mode != car || driving.Duration <= time.Duration(city.CarOverheadMinutes)*time.Minute {
		t.Fatalf("drive %+v %v", driving, ok)
	}
}

func straightMeters(a, b domain.Coordinate) float64 {
	rad := math.Pi / 180
	dLat, dLon := (b.Latitude-a.Latitude)*rad, (b.Longitude-a.Longitude)*rad
	h := math.Pow(math.Sin(dLat/2), 2) + math.Cos(a.Latitude*rad)*math.Cos(b.Latitude*rad)*math.Pow(math.Sin(dLon/2), 2)
	return 2 * 6371000 * math.Asin(math.Sqrt(h))
}
