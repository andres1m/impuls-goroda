package routing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/solver"
)

const threePointTable = `{"code":"Ok","data_version":"20260926T202251Z-f77a89d0",
	"durations":[[0,150,400],[150,0,300],[400,300,0]],
	"distances":[[0,1500,3300],[1500,0,2800],[3300,2800,0]],
	"sources":[{"distance":3},{"distance":5},{"distance":8}]}`

type fakeRouter struct {
	*httptest.Server
	calls atomic.Int32
}

func newFakeRouter(t *testing.T, status int, body string) *fakeRouter {
	t.Helper()
	f := &fakeRouter{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.calls.Add(1)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.Close)
	return f
}

func newTestRouter(t *testing.T, foot, car *fakeRouter) *Router {
	t.Helper()
	cfg := config()
	city := cityConfig()
	city.Foot, city.Car = foot.URL, car.URL
	cfg.Cities = map[string]CityConfig{"perm": city}
	r, err := NewRouter(cfg, NewClient(http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRouterUsesTheCityRouters(t *testing.T) {
	footRouter := newFakeRouter(t, http.StatusOK, threePointTable)
	carRouter := newFakeRouter(t, http.StatusOK, threePointTable)
	rule, degraded, err := newTestRouter(t, footRouter, carRouter).Transit(context.Background(), "perm", points, modes(walk, transit))
	if err != nil || degraded {
		t.Fatalf("degraded=%v err=%v", degraded, err)
	}
	if _, ok := rule.(*MatrixTransit); !ok {
		t.Fatalf("transit is %T", rule)
	}
	if got, ok := rule.Estimate(origin, far, graphTime, modes(walk, transit)); !ok || got.Verification != domain.VerificationEstimated {
		t.Fatalf("estimate %+v %v", got, ok)
	}
	if footRouter.calls.Load() != 1 || carRouter.calls.Load() != 1 {
		t.Fatalf("calls foot=%d car=%d", footRouter.calls.Load(), carRouter.calls.Load())
	}
}

func TestRouterAsksOnlyForNeededGraphs(t *testing.T) {
	footRouter := newFakeRouter(t, http.StatusOK, threePointTable)
	carRouter := newFakeRouter(t, http.StatusOK, threePointTable)
	r := newTestRouter(t, footRouter, carRouter)
	if _, _, err := r.Transit(context.Background(), "perm", points, modes(walk)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Transit(context.Background(), "perm", points, modes(car)); err != nil {
		t.Fatal(err)
	}
	if footRouter.calls.Load() != 1 || carRouter.calls.Load() != 1 {
		t.Fatalf("calls foot=%d car=%d", footRouter.calls.Load(), carRouter.calls.Load())
	}
}

func TestRouterFallsBackToStraightLines(t *testing.T) {
	footRouter := newFakeRouter(t, http.StatusOK, threePointTable)
	carRouter := newFakeRouter(t, http.StatusInternalServerError, `{"code":"InternalError","message":"down"}`)
	before := degradations(t)
	rule, degraded, err := newTestRouter(t, footRouter, carRouter).Transit(context.Background(), "perm", points, modes(walk, transit, car))
	if err != nil || !degraded {
		t.Fatalf("degraded=%v err=%v", degraded, err)
	}
	if _, ok := rule.(*solver.BaselineTransit); !ok {
		t.Fatalf("fallback is %T", rule)
	}
	got, ok := rule.Estimate(origin, far, graphTime, modes(walk, transit, car))
	if !ok || got.Verification != domain.VerificationUnknown || got.Mode == domain.MovementCar {
		t.Fatalf("fallback estimate %+v %v", got, ok)
	}
	if _, ok := rule.Estimate(origin, far, graphTime, modes(car)); ok {
		t.Fatal("car offered without a router")
	}
	if degradations(t) != before+1 {
		t.Fatal("degradation not counted")
	}
}

func TestRouterRejectsUnknownCity(t *testing.T) {
	footRouter := newFakeRouter(t, http.StatusOK, threePointTable)
	carRouter := newFakeRouter(t, http.StatusOK, threePointTable)
	if _, _, err := newTestRouter(t, footRouter, carRouter).Transit(context.Background(), "kazan", points, modes(walk)); err == nil {
		t.Fatal("unknown city accepted")
	}
}

func TestRouterStopsOnCancelledContext(t *testing.T) {
	footRouter := newFakeRouter(t, http.StatusOK, threePointTable)
	carRouter := newFakeRouter(t, http.StatusOK, threePointTable)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := newTestRouter(t, footRouter, carRouter).Transit(ctx, "perm", points, modes(walk))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestNewRouterRejectsInvalidConfig(t *testing.T) {
	if _, err := NewRouter(Config{}, NewClient(http.DefaultClient)); err == nil {
		t.Fatal("invalid config accepted")
	}
}

func degradations(t *testing.T) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "routing_degraded_total" {
			continue
		}
		for _, m := range family.GetMetric() {
			for _, label := range m.GetLabel() {
				if label.GetName() == "city" && label.GetValue() == "perm" {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

func TestRouterStopsWhenCancelledDuringRequest(t *testing.T) {
	started := make(chan struct{})
	slow := &fakeRouter{}
	slow.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	t.Cleanup(slow.Close)
	r := newTestRouter(t, slow, newFakeRouter(t, http.StatusOK, threePointTable))
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	before := degradations(t)
	_, degraded, err := r.Transit(ctx, "perm", points, modes(walk))
	if !errors.Is(err, context.Canceled) || degraded {
		t.Fatalf("degraded=%v err=%v", degraded, err)
	}
	if degradations(t) != before {
		t.Fatal("a cancelled search was counted as degraded")
	}
}
