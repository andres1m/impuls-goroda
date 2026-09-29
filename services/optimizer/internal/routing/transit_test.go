package routing

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

const graphVersion = "20260926T202251Z-f77a89d0"

var (
	origin = domain.Coordinate{Longitude: 56.2294, Latitude: 58.0105}
	near   = domain.Coordinate{Longitude: 56.2400, Latitude: 58.0110}
	far    = domain.Coordinate{Longitude: 56.3100, Latitude: 58.0500}
	points = []domain.Coordinate{origin, near, far}

	graphTime = time.Date(2026, 9, 26, 20, 22, 51, 0, time.UTC)
)

// table builds a routing table where NaN marks a pair without a path.
func table(snap []float64, seconds, meters [][]float64) *Table {
	t := &Table{Version: graphVersion, SnapMeters: snap, size: len(snap)}
	for i := range seconds {
		for j := range seconds[i] {
			s, m := seconds[i][j], meters[i][j]
			if math.IsNaN(s) {
				t.seconds, t.meters = append(t.seconds, nil), append(t.meters, nil)
				continue
			}
			t.seconds, t.meters = append(t.seconds, &s), append(t.meters, &m)
		}
	}
	return t
}

func footTable() *Table {
	return table([]float64{3, 5, 8},
		[][]float64{{0, 1200, 2400}, {1200, 0, 2000}, {2400, 2000, 0}},
		[][]float64{{0, 1500, 3000}, {1500, 0, 2500}, {3000, 2500, 0}})
}

func carTable() *Table {
	return table([]float64{3, 5, 8},
		[][]float64{{0, 150, 400}, {150, 0, 300}, {400, 300, 0}},
		[][]float64{{0, 1700, 3300}, {1700, 0, 2800}, {3300, 2800, 0}})
}

func matrix(t *testing.T, foot, car *Table) *MatrixTransit {
	t.Helper()
	m, err := newMatrixTransit(points, foot, car, cityConfig(), 500)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func modes(m ...domain.MovementMode) []domain.MovementMode { return m }

var (
	walk    = domain.MovementWalk
	transit = domain.MovementTransit
	car     = domain.MovementCar
)

func TestMatrixTransitChoosesMode(t *testing.T) {
	both := matrix(t, footTable(), carTable())
	cases := []struct {
		name     string
		to       domain.Coordinate
		modes    []domain.MovementMode
		mode     domain.MovementMode
		meters   float64
		duration time.Duration
	}{
		{"short walk", near, modes(walk, transit), walk, 1500, 20 * time.Minute},
		{"long distance takes transit", far, modes(walk, transit), transit, 3300, 23 * time.Minute},
		{"car beats transit", far, modes(walk, transit, car), car, 3300, 18*time.Minute + 40*time.Second},
		{"short walk even when car is allowed", near, modes(walk, car), walk, 1500, 20 * time.Minute},
		{"walk only never switches", far, modes(walk), walk, 3000, 40 * time.Minute},
		{"transit only on a short distance", near, modes(transit), transit, 1700, 17*time.Minute + 40*time.Second},
		{"car only", near, modes(car), car, 1700, 13*time.Minute + 15*time.Second},
	}
	for _, tc := range cases {
		got, ok := both.Estimate(origin, tc.to, graphTime, tc.modes)
		if !ok || got.Mode != tc.mode || got.DistanceMeters != tc.meters || got.Duration != tc.duration {
			t.Errorf("%s: got %+v ok=%v", tc.name, got, ok)
		}
	}
}

func TestMatrixTransitWithoutPath(t *testing.T) {
	foot := footTable()
	foot.seconds[2], foot.meters[2] = nil, nil
	roads := carTable()
	roads.seconds[2], roads.meters[2] = nil, nil
	m := matrix(t, foot, roads)
	if _, ok := m.Estimate(origin, far, graphTime, modes(walk)); ok {
		t.Error("walk without a path in the graph")
	}
	if _, ok := m.Estimate(origin, far, graphTime, modes(walk, transit, car)); ok {
		t.Error("travel without a path in any graph")
	}
	onlyFootGap := matrix(t, foot, carTable())
	if got, ok := onlyFootGap.Estimate(origin, far, graphTime, modes(walk, transit)); !ok || got.Mode != transit {
		t.Errorf("no walking path should fall back to transit: %+v %v", got, ok)
	}
	onlyCarGap := matrix(t, footTable(), roads)
	if got, ok := onlyCarGap.Estimate(origin, far, graphTime, modes(walk, transit)); !ok || got.Mode != walk {
		t.Errorf("no road path should leave the long walk: %+v %v", got, ok)
	}
}

func TestMatrixTransitRefusesPointsOffTheNetwork(t *testing.T) {
	foot, roads := footTable(), carTable()
	foot.SnapMeters[2], roads.SnapMeters[2] = 501, 501
	m := matrix(t, foot, roads)
	if _, ok := m.Estimate(origin, far, graphTime, modes(walk, transit, car)); ok {
		t.Error("point 501 m from the network was routed")
	}
	if _, ok := m.Estimate(origin, near, graphTime, modes(walk)); !ok {
		t.Error("points on the network were refused")
	}
	unknown := domain.Coordinate{Longitude: 56.5, Latitude: 58.1}
	if _, ok := m.Estimate(origin, unknown, graphTime, modes(walk)); ok {
		t.Error("point outside the table was routed")
	}
}

func TestMatrixTransitWithoutNeededGraph(t *testing.T) {
	m := matrix(t, footTable(), nil)
	if _, ok := m.Estimate(origin, far, graphTime, modes(car)); ok {
		t.Error("car estimate without a road graph")
	}
}

func TestMatrixTransitEvidence(t *testing.T) {
	m := matrix(t, footTable(), carTable())
	cases := map[domain.MovementMode]struct {
		to         domain.Coordinate
		method     string
		limitation string
	}{
		domain.MovementWalk:    {near, "osm_foot_network", "time_is_modelled"},
		domain.MovementTransit: {far, "osm_road_network", "transit_estimated_from_road_distance"},
		domain.MovementCar:     {far, "osm_road_network", "no_live_traffic"},
	}
	for mode, tc := range cases {
		got, ok := m.Estimate(origin, tc.to, graphTime.Add(time.Hour), modes(mode))
		if !ok || got.Verification != domain.VerificationEstimated {
			t.Fatalf("%s: %+v %v", mode, got, ok)
		}
		e := got.Evidence
		if err := e.Validate(); err != nil || e.Provider != "osrm" || e.Method != tc.method ||
			!e.ObservedAt.Equal(graphTime) ||
			e.Mode != string(mode) ||
			!slices.Contains(e.Limitations, tc.limitation) ||
			!slices.Contains(e.Limitations, "osm_data_may_be_outdated") {
			t.Errorf("%s: evidence %+v: %v", mode, e, err)
		}
	}
}

func TestMatrixTransitRejectsUnknownVersion(t *testing.T) {
	foot := footTable()
	foot.Version = "latest"
	if _, err := newMatrixTransit(points, foot, nil, cityConfig(), 500); err == nil {
		t.Fatal("graph without a dated version accepted")
	}
}
