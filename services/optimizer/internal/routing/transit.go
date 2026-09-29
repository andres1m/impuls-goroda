package routing

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// MatrixTransit answers travel between the points of one search from routing tables fetched
// in advance, so the search itself never waits on the network. Travel does not depend on the
// departure time: the graphs carry no timetable or traffic.
type MatrixTransit struct {
	points     map[domain.Coordinate]int
	foot, car  *graphTable
	city       CityConfig
	snapMeters float64
}

type graphTable struct {
	*Table
	observedAt time.Time
}

// newMatrixTransit takes the walking and road tables over points; either may be nil when no
// allowed mode needs it.
const secondsPerMinute = 60

//nolint:gocritic // transit keeps a snapshot of the city configuration
func newMatrixTransit(
	points []domain.Coordinate,
	foot, car *Table,
	city CityConfig,
	snapMeters float64,
) (*MatrixTransit, error) {
	m := &MatrixTransit{points: make(map[domain.Coordinate]int, len(points)), city: city, snapMeters: snapMeters}
	for i, p := range points {
		m.points[p] = i
	}
	var err error
	if m.foot, err = dated(foot); err != nil {
		return nil, err
	}
	if m.car, err = dated(car); err != nil {
		return nil, err
	}
	return m, nil
}

func dated(t *Table) (*graphTable, error) {
	if t == nil {
		return nil, nil
	}
	stamp, _, _ := strings.Cut(t.Version, "-")
	observedAt, err := time.Parse("20060102T150405Z", stamp)
	if err != nil {
		return nil, fmt.Errorf("routing graph version %q carries no data date", t.Version)
	}
	return &graphTable{Table: t, observedAt: observedAt}, nil
}

func (m *MatrixTransit) Estimate(
	from, to domain.Coordinate,
	_ time.Time,
	modes []domain.MovementMode,
) (domain.TransitEstimate, bool) {
	i, okFrom := m.points[from]
	j, okTo := m.points[to]
	if !okFrom || !okTo {
		return domain.TransitEstimate{}, false
	}
	walk, canWalk := m.walk(i, j, modes)
	if canWalk && walk.DistanceMeters <= m.city.WalkThresholdMeters {
		return walk, true
	}
	var best domain.TransitEstimate
	found := false
	for _, option := range []func(int, int, []domain.MovementMode) (domain.TransitEstimate, bool){m.transit, m.drive} {
		if e, ok := option(i, j, modes); ok && (!found || e.Duration < best.Duration) {
			best, found = e, true
		}
	}
	if found {
		return best, true
	}
	// Walking stays the answer when the user may walk and nothing faster can make the trip.
	return walk, canWalk
}

func (m *MatrixTransit) walk(i, j int, modes []domain.MovementMode) (domain.TransitEstimate, bool) {
	if !slices.Contains(modes, domain.MovementWalk) {
		return domain.TransitEstimate{}, false
	}
	_, meters, ok := m.pair(m.foot, i, j)
	if !ok {
		return domain.TransitEstimate{}, false
	}
	return m.estimate(domain.MovementWalk, meters, meters/m.city.WalkMetersPerMinute, m.foot, "osm_foot_network"), true
}

func (m *MatrixTransit) transit(i, j int, modes []domain.MovementMode) (domain.TransitEstimate, bool) {
	if !slices.Contains(modes, domain.MovementTransit) {
		return domain.TransitEstimate{}, false
	}
	_, meters, ok := m.pair(m.car, i, j)
	if !ok {
		return domain.TransitEstimate{}, false
	}
	minutes := m.city.TransitWaitMinutes + meters/m.city.TransitMetersPerMinute
	return m.estimate(
		domain.MovementTransit,
		meters,
		minutes,
		m.car,
		"osm_road_network",
		"transit_estimated_from_road_distance",
	), true
}

func (m *MatrixTransit) drive(i, j int, modes []domain.MovementMode) (domain.TransitEstimate, bool) {
	if !slices.Contains(modes, domain.MovementCar) {
		return domain.TransitEstimate{}, false
	}
	seconds, meters, ok := m.pair(m.car, i, j)
	if !ok {
		return domain.TransitEstimate{}, false
	}
	minutes := m.city.CarOverheadMinutes + seconds/60*m.city.CarCongestionFactor
	return m.estimate(domain.MovementCar, meters, minutes, m.car, "osm_road_network", "no_live_traffic"), true
}

// pair refuses points the router had to move farther than the snap radius: the path would
// start at some other street, not at the place.
func (m *MatrixTransit) pair(t *graphTable, i, j int) (seconds, meters float64, ok bool) {
	if t == nil || t.SnapMeters[i] > m.snapMeters || t.SnapMeters[j] > m.snapMeters {
		return 0, 0, false
	}
	return t.Pair(i, j)
}

func (m *MatrixTransit) estimate(
	mode domain.MovementMode,
	meters, minutes float64,
	t *graphTable,
	method string,
	limitations ...string,
) domain.TransitEstimate {
	return domain.TransitEstimate{
		Mode:           mode,
		DistanceMeters: meters,
		Duration:       time.Duration(math.Round(minutes*secondsPerMinute)) * time.Second,
		// OpenStreetMap shows a street existed when the data was taken, not that it is open now.
		Verification: domain.VerificationEstimated,
		Evidence: domain.LegEvidence{
			Provider:    "osrm",
			Method:      method,
			ObservedAt:  t.observedAt,
			Mode:        string(mode),
			Limitations: append([]string{"osm_data_may_be_outdated", "time_is_modelled"}, limitations...),
		},
	}
}
