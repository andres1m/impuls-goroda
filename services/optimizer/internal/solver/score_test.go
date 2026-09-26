package solver

import (
	"math"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func TestAffinity(t *testing.T) {
	p := DefaultScoreParams()
	history := domain.ArchetypeHistoryHeritage
	classical := domain.Interests(domain.InterestClassicalArt)
	all := domain.Interests(domain.InterestClassicalArt, domain.InterestExcursions, domain.InterestCinema)
	cases := []struct {
		name        string
		user, visit domain.InterestMask
		want        float64
	}{
		{"empty user mask", 0, domain.Interests(domain.InterestCinema), 1},
		{"empty user mask with archetype bonus", 0, classical, 1.5},
		{"one match", domain.Interests(domain.InterestCinema), domain.Interests(domain.InterestCinema), 3},
		{"three matches with archetype bonus", all, all, 7.5},
		{"no match", domain.Interests(domain.InterestCinema), domain.Interests(domain.InterestStreetWorkout), 0.15},
		{"no match with archetype bonus", domain.Interests(domain.InterestCinema), classical, 0.225},
	}
	for _, tc := range cases {
		if got := p.affinity(tc.user, tc.visit, history); math.Abs(got-tc.want) > 1e-9 {
			t.Fatalf("%s: affinity = %f, want %f", tc.name, got, tc.want)
		}
	}
}

func TestGain(t *testing.T) {
	p := DefaultScoreParams()
	if got := p.gain(3, 10*time.Minute, 5*time.Minute, 2); math.Abs(got-(-3.5)) > 1e-9 {
		t.Fatalf("gain = %f, want -3.5", got)
	}
	if got := p.gain(1, 0, 0, 0); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("first visit of a category gain = %f, want 0.5", got)
	}
}
