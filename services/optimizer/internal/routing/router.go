package routing

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"golang.org/x/sync/errgroup"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/solver"
)

var (
	tableSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "routing_table_duration_seconds",
		Help: "Time to fetch one routing table from a city router.",
	}, []string{"city", "profile"})
	routingErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "routing_errors_total",
		Help: "Routing table requests that failed.",
	}, []string{"city", "profile"})
	degradedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "routing_degraded_total",
		Help: "Searches that fell back to straight-line travel estimates.",
	}, []string{"city"})
)

// Router picks the routing graphs of a city for a search.
type Router struct {
	cfg    Config
	client *Client
}

func NewRouter(cfg Config, client *Client) (*Router, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Router{cfg: cfg, client: client}, nil
}

// Transit prepares travel between the given points for one search. When the city's routers
// cannot answer, straight-line formulas stand in, their estimates are unknown and driving is not
// offered; degraded reports that. An error means the request itself cannot be served.
func (r *Router) Transit(ctx context.Context, city string, points []domain.Coordinate, modes []domain.MovementMode) (transit solver.Transit, degraded bool, err error) {
	cfg, ok := r.cfg.Cities[city]
	if !ok {
		return nil, false, fmt.Errorf("no routing for city %q", city)
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	var foot, car *Table
	g, gctx := errgroup.WithContext(ctx)
	if slices.Contains(modes, domain.MovementWalk) {
		g.Go(func() (err error) { foot, err = r.table(gctx, city, "foot", cfg.Foot, points); return err })
	}
	if slices.Contains(modes, domain.MovementTransit) || slices.Contains(modes, domain.MovementCar) {
		g.Go(func() (err error) { car, err = r.table(gctx, city, "car", cfg.Car, points); return err })
	}
	err = g.Wait()
	if err == nil {
		var matrix *MatrixTransit
		if matrix, err = newMatrixTransit(points, foot, car, cfg, r.cfg.SnapRadiusMeters); err == nil {
			return matrix, false, nil
		}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, false, ctxErr
	}
	degradedTotal.WithLabelValues(city).Inc()
	fallback, err := solver.NewBaselineTransit(baselineParams(cfg))
	if err != nil {
		return nil, false, err
	}
	return fallback, true, nil
}

func (r *Router) table(ctx context.Context, city, profile, endpoint string, points []domain.Coordinate) (*Table, error) {
	requestCtx, cancel := context.WithTimeout(ctx, r.cfg.RequestTimeout)
	defer cancel()
	start := time.Now()
	t, err := r.client.Table(requestCtx, endpoint, profile, points)
	tableSeconds.WithLabelValues(city, profile).Observe(time.Since(start).Seconds())
	if err != nil {
		// A request cut short by the caller or by the other profile's failure says nothing about this router.
		if ctx.Err() == nil {
			routingErrors.WithLabelValues(city, profile).Inc()
		}
		return nil, err
	}
	return &t, nil
}

// baselineParams keeps the city's speeds and the normative detour factors, which stand in for
// the street network the fallback cannot see.
func baselineParams(cfg CityConfig) solver.TransitParams {
	p := solver.DefaultTransitParams()
	p.WalkThresholdMeters = cfg.WalkThresholdMeters
	p.WalkMetersPerMinute = cfg.WalkMetersPerMinute
	p.TransitWaitMinutes = cfg.TransitWaitMinutes
	p.TransitMetersPerMinute = cfg.TransitMetersPerMinute
	return p
}
