package solver

import (
	"errors"
	"math"
)

type Config struct {
	BeamWidth   int
	Parallelism int
}

func (c Config) Validate() error {
	if c.BeamWidth <= 0 || c.Parallelism <= 0 {
		return errors.New("beam width and parallelism must be positive")
	}
	return nil
}

// TransitParams are per-city constants of the straight-line transit estimate.
type TransitParams struct {
	WalkThresholdMeters    float64
	WalkDetour             float64
	WalkMetersPerMinute    float64
	TransitWaitMinutes     float64
	TransitDetour          float64
	TransitMetersPerMinute float64
}

func DefaultTransitParams() TransitParams {
	return TransitParams{
		WalkThresholdMeters:    1800,
		WalkDetour:             1.25,
		WalkMetersPerMinute:    75,
		TransitWaitMinutes:     12,
		TransitDetour:          1.15,
		TransitMetersPerMinute: 300,
	}
}

func (p TransitParams) Validate() error {
	if !positive(p.WalkDetour, p.WalkMetersPerMinute, p.TransitDetour, p.TransitMetersPerMinute) ||
		!nonNegative(p.WalkThresholdMeters, p.TransitWaitMinutes) {
		return errors.New("transit parameters must be finite with positive speeds and detours")
	}
	return nil
}

type ScoreParams struct {
	AffinityBase   float64
	AffinityScale  float64
	NoMatchFactor  float64
	ArchetypeBonus float64
	WaitWeight     float64
	TransitWeight  float64
	CategoryWeight float64
}

func DefaultScoreParams() ScoreParams {
	return ScoreParams{
		AffinityBase:   1,
		AffinityScale:  2,
		NoMatchFactor:  0.15,
		ArchetypeBonus: 1.5,
		WaitWeight:     0.3,
		TransitWeight:  0.2,
		CategoryWeight: 0.5,
	}
}

func (p ScoreParams) Validate() error {
	if !positive(p.ArchetypeBonus) ||
		!nonNegative(p.AffinityBase, p.AffinityScale, p.NoMatchFactor, p.WaitWeight, p.TransitWeight, p.CategoryWeight) {
		return errors.New("score parameters must be finite and non-negative with a positive archetype bonus")
	}
	return nil
}

func positive(values ...float64) bool {
	for _, v := range values {
		if !(v > 0) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

func nonNegative(values ...float64) bool {
	for _, v := range values {
		if !(v >= 0) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}
