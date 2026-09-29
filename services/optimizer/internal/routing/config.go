// Package routing turns the OSRM routers of each city into travel estimates for the search.
package routing

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"time"
)

type Config struct {
	RequestTimeout time.Duration `yaml:"request-timeout"`
	// A point farther than this from any street cannot be routed.
	SnapRadiusMeters float64               `yaml:"snap-radius-meters"`
	Cities           map[string]CityConfig `yaml:"cities"`
}

// CityConfig names the routers serving a city and its travel-time constants.
type CityConfig struct {
	Foot                   string  `yaml:"foot"`
	Car                    string  `yaml:"car"`
	WalkThresholdMeters    float64 `yaml:"walk-threshold-meters"`
	WalkMetersPerMinute    float64 `yaml:"walk-meters-per-minute"`
	TransitWaitMinutes     float64 `yaml:"transit-wait-minutes"`
	TransitMetersPerMinute float64 `yaml:"transit-meters-per-minute"`
	CarOverheadMinutes     float64 `yaml:"car-overhead-minutes"`
	CarCongestionFactor    float64 `yaml:"car-congestion-factor"`
}

func (c Config) Validate() error {
	if c.RequestTimeout <= 0 {
		return errors.New("routing request timeout must be positive")
	}
	if !positive(c.SnapRadiusMeters) {
		return errors.New("routing snap radius must be positive")
	}
	if len(c.Cities) == 0 {
		return errors.New("routing needs at least one city")
	}
	for city, cfg := range c.Cities {
		if err := cfg.validate(); err != nil {
			return fmt.Errorf("routing city %s: %w", city, err)
		}
	}
	return nil
}

func (c *CityConfig) validate() error {
	for _, endpoint := range []string{c.Foot, c.Car} {
		u, err := url.Parse(endpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("router endpoint %q must be an absolute http URL", endpoint)
		}
	}
	if !positive(c.WalkMetersPerMinute) || !positive(c.TransitMetersPerMinute) || !positive(c.CarCongestionFactor) {
		return errors.New("speeds and congestion factor must be positive")
	}
	if !nonNegative(c.WalkThresholdMeters) || !nonNegative(c.TransitWaitMinutes) || !nonNegative(c.CarOverheadMinutes) {
		return errors.New("threshold, wait and overhead must not be negative")
	}
	return nil
}

func positive(v float64) bool {
	return v > 0 && !math.IsInf(v, 0)
}

func nonNegative(v float64) bool {
	return v >= 0 && !math.IsInf(v, 0)
}
