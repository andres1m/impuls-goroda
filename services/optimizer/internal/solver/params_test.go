package solver

import (
	"math"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func TestParamsValidate(t *testing.T) {
	if err := DefaultTransitParams().Validate(); err != nil {
		t.Fatalf("default transit params: %v", err)
	}
	if err := DefaultScoreParams().Validate(); err != nil {
		t.Fatalf("default score params: %v", err)
	}
	if err := (Config{BeamWidth: 1, Parallelism: 1}).Validate(); err != nil {
		t.Fatalf("minimal config: %v", err)
	}
	transit := []func(*TransitParams){
		func(p *TransitParams) { p.WalkMetersPerMinute = 0 },
		func(p *TransitParams) { p.TransitMetersPerMinute = -1 },
		func(p *TransitParams) { p.WalkDetour = math.NaN() },
		func(p *TransitParams) { p.TransitDetour = math.Inf(1) },
		func(p *TransitParams) { p.WalkThresholdMeters = -1 },
		func(p *TransitParams) { p.TransitWaitMinutes = math.NaN() },
	}
	for i, change := range transit {
		p := DefaultTransitParams()
		change(&p)
		if p.Validate() == nil {
			t.Fatalf("transit case %d accepted", i)
		}
	}
	score := []func(*ScoreParams){
		func(p *ScoreParams) { p.ArchetypeBonus = 0 },
		func(p *ScoreParams) { p.WaitWeight = -0.1 },
		func(p *ScoreParams) { p.AffinityScale = math.NaN() },
		func(p *ScoreParams) { p.CategoryWeight = math.Inf(1) },
	}
	for i, change := range score {
		p := DefaultScoreParams()
		change(&p)
		if p.Validate() == nil {
			t.Fatalf("score case %d accepted", i)
		}
	}
	for _, cfg := range []Config{{}, {BeamWidth: 1}, {Parallelism: 1}, {BeamWidth: -1, Parallelism: 1}} {
		if cfg.Validate() == nil {
			t.Fatalf("config %+v accepted", cfg)
		}
	}
}

func TestDefaultParams(t *testing.T) {
	transit := TransitParams{WalkThresholdMeters: 1800, WalkDetour: 1.25, WalkMetersPerMinute: 75, TransitWaitMinutes: 12, TransitDetour: 1.15, TransitMetersPerMinute: 300}
	if DefaultTransitParams() != transit {
		t.Fatalf("transit defaults = %+v", DefaultTransitParams())
	}
	score := ScoreParams{AffinityBase: 1, AffinityScale: 2, NoMatchFactor: 0.15, ArchetypeBonus: 1.5, WaitWeight: 0.3, TransitWeight: 0.2, CategoryWeight: 0.5}
	if DefaultScoreParams() != score {
		t.Fatalf("score defaults = %+v", DefaultScoreParams())
	}
}

func TestProblemValidate(t *testing.T) {
	if err := problem().Validate(); err != nil {
		t.Fatalf("valid problem: %v", err)
	}
	cases := map[string]func(*Problem){
		"no start":     func(p *Problem) { p.Start = time.Time{} },
		"inverted":     func(p *Problem) { p.End = p.Start },
		"bad origin":   func(p *Problem) { p.Origin.Latitude = 91 },
		"no archetype": func(p *Problem) { p.Archetype = "" },
		"no modes":     func(p *Problem) { p.Modes = nil },
		"unknown mode": func(p *Problem) { p.Modes = []domain.MovementMode{"bike"} },
		"bad currency": func(p *Problem) { p.Currency = "rub" },
	}
	for name, change := range cases {
		p := problem()
		change(&p)
		if p.Validate() == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}
