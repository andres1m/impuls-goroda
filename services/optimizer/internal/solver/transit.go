package solver

import (
	"math"
	"slices"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// Transit estimates how to get between two points when leaving at departAt.
type Transit interface {
	Estimate(from, to domain.Coordinate, departAt time.Time, modes []domain.MovementMode) (domain.TransitEstimate, bool)
}

// BaselineTransit is a straight-line estimate that ignores the departure time, bridges and entrances.
type BaselineTransit struct {
	params TransitParams
}

func NewBaselineTransit(params TransitParams) (*BaselineTransit, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	return &BaselineTransit{params: params}, nil
}

func (t *BaselineTransit) Estimate(from, to domain.Coordinate, _ time.Time, modes []domain.MovementMode) (domain.TransitEstimate, bool) {
	walk := slices.Contains(modes, domain.MovementWalk)
	transit := slices.Contains(modes, domain.MovementTransit)
	d := distanceMeters(from, to)
	p := t.params

	var mode domain.MovementMode
	var minutes float64
	switch {
	case walk && (!transit || d <= p.WalkThresholdMeters):
		mode, minutes = domain.MovementWalk, d*p.WalkDetour/p.WalkMetersPerMinute
	case transit:
		mode, minutes = domain.MovementTransit, p.TransitWaitMinutes+d*p.TransitDetour/p.TransitMetersPerMinute
	default:
		return domain.TransitEstimate{}, false
	}
	return domain.TransitEstimate{
		Mode:           mode,
		DistanceMeters: d,
		Duration:       time.Duration(math.Round(minutes*60)) * time.Second,
		Verification:   domain.VerificationEstimated,
	}, true
}

const earthRadiusMeters = 6_371_000

func distanceMeters(a, b domain.Coordinate) float64 {
	lat1, lat2 := radians(a.Latitude), radians(b.Latitude)
	sinLat := math.Sin((lat2 - lat1) / 2)
	sinLon := math.Sin(radians(b.Longitude-a.Longitude) / 2)
	h := sinLat*sinLat + math.Cos(lat1)*math.Cos(lat2)*sinLon*sinLon
	return 2 * earthRadiusMeters * math.Asin(math.Sqrt(min(h, 1)))
}

func radians(degrees float64) float64 {
	return degrees * math.Pi / 180
}
