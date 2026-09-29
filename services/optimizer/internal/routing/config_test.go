package routing

import (
	"testing"
	"time"
)

func cityConfig() CityConfig {
	return CityConfig{
		Foot:                   "http://osrm-foot:5000",
		Car:                    "http://osrm-car:5000",
		WalkThresholdMeters:    1800,
		WalkMetersPerMinute:    75,
		TransitWaitMinutes:     12,
		TransitMetersPerMinute: 300,
		CarOverheadMinutes:     10,
		CarCongestionFactor:    1.3,
	}
}

func config() Config {
	return Config{
		RequestTimeout:   2 * time.Second,
		SnapRadiusMeters: 500,
		Cities:           map[string]CityConfig{"perm": cityConfig()},
	}
}

func TestConfigValidate(t *testing.T) {
	if err := config().Validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	cases := map[string]func(*Config){
		"no timeout":         func(c *Config) { c.RequestTimeout = 0 },
		"no snap radius":     func(c *Config) { c.SnapRadiusMeters = 0 },
		"no cities":          func(c *Config) { c.Cities = nil },
		"relative endpoint":  func(c *Config) { city := cityConfig(); city.Foot = "osrm-foot:5000"; c.Cities["perm"] = city },
		"missing endpoint":   func(c *Config) { city := cityConfig(); city.Car = ""; c.Cities["perm"] = city },
		"zero walk speed":    func(c *Config) { city := cityConfig(); city.WalkMetersPerMinute = 0; c.Cities["perm"] = city },
		"zero transit speed": func(c *Config) { city := cityConfig(); city.TransitMetersPerMinute = 0; c.Cities["perm"] = city },
		"negative wait":      func(c *Config) { city := cityConfig(); city.TransitWaitMinutes = -1; c.Cities["perm"] = city },
		"negative overhead":  func(c *Config) { city := cityConfig(); city.CarOverheadMinutes = -1; c.Cities["perm"] = city },
		"zero congestion":    func(c *Config) { city := cityConfig(); city.CarCongestionFactor = 0; c.Cities["perm"] = city },
		"negative threshold": func(c *Config) { city := cityConfig(); city.WalkThresholdMeters = -1; c.Cities["perm"] = city },
	}
	for name, change := range cases {
		c := config()
		c.Cities = map[string]CityConfig{"perm": cityConfig()}
		change(&c)
		if c.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
