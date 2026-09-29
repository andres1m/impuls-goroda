package usecase

import (
	"context"
	"net/http"
	"os"
	"slices"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/routing"
)

// A day in central Perm with a committed session across the Kama, planned over the live routers.
func TestOptimizeOverLiveRoutingIntegration(t *testing.T) {
	footURL, carURL := os.Getenv("OPTIMIZER_TEST_OSRM_FOOT_URL"), os.Getenv("OPTIMIZER_TEST_OSRM_CAR_URL")
	if footURL == "" || carURL == "" {
		t.Skip("OPTIMIZER_TEST_OSRM_FOOT_URL and OPTIMIZER_TEST_OSRM_CAR_URL are not set")
	}
	city := routing.CityConfig{
		Foot: footURL, Car: carURL, WalkThresholdMeters: 1800, WalkMetersPerMinute: 75,
		TransitWaitMinutes: 12, TransitMetersPerMinute: 300, CarOverheadMinutes: 10, CarCongestionFactor: 1.3,
	}
	router, err := routing.NewRouter(
		routing.Config{
			RequestTimeout:   5 * time.Second,
			SnapRadiusMeters: 500,
			Cities:           map[string]routing.CityConfig{"perm": city},
		},
		routing.NewClient(&http.Client{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	centre := domain.Coordinate{Longitude: 56.2294, Latitude: 58.0105}
	candidates := []domain.Candidate{
		place(1, domain.CategoryCulture, domain.Coordinate{Longitude: 56.2445, Latitude: 58.0135}),
		place(2, domain.CategoryWalk, domain.Coordinate{Longitude: 56.2380, Latitude: 58.0080}),
		session(
			3,
			domain.CategoryCulture,
			domain.Coordinate{Longitude: 56.3100, Latitude: 58.0500},
			at(15, 0),
			at(16, 0),
			40000,
		),
	}
	req := request()
	req.Origin, req.Destination = centre, nil
	req.Constraints.Obligations = []domain.Obligation{
		{SessionID: &domain.SessionID{3}, Participation: domain.ParticipationUserReported},
	}
	p, err := NewPlanner(config(), fakeSource{candidates: candidates}, router, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := p.Optimize(ctx, &req)
	if err != nil {
		t.Fatal(err)
	}
	err = res.Validate()
	if err != nil || res.Status != domain.ResultReady || len(res.Routes) == 0 {
		t.Fatalf(
			"status %s routes %d conflicts %v warnings %v err %v",
			res.Status,
			len(res.Routes),
			res.Conflicts,
			res.Warnings,
			err,
		)
	}
	recompReq := domain.RecomputeRequest{
		City:        req.City,
		Timezone:    req.Timezone,
		Base:        res.Routes[0],
		Constraints: req.Constraints,
		Trigger: domain.DelayTrigger{
			Mode:           domain.DelayAlreadyDelayed,
			EffectiveStart: req.Start.Add(30 * time.Minute),
			Position:       centre,
			PositionSource: domain.PositionDevice,
		},
	}
	late, err := p.Recompute(ctx, &recompReq)
	if err != nil {
		t.Fatal(err)
	}
	if err := late.Validate(); err != nil || late.Status == domain.RecomputeConflict {
		t.Fatalf("recompute after a delay: status %s conflicts %v err %v", late.Status, late.Conflicts, err)
	}
	for _, route := range res.Routes {
		if !slices.ContainsFunc(route.Steps, func(s domain.Step) bool { return s.Obligation }) {
			t.Fatal("route lost the committed session")
		}
		for _, leg := range route.Legs {
			if leg.Verification != domain.VerificationEstimated || leg.Evidence.Provider != "osrm" {
				t.Fatalf("leg %+v", leg)
			}
		}
	}
}
