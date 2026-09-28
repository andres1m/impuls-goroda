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
	// half an hour at affinity 3, ten minutes of waiting, five minutes of travel, third visit of a category
	if got := p.gain(3, 30*time.Minute, 10*time.Minute, 0.2*5, 2); math.Abs(got-(0.5*3*30-3-1-5*5)) > 1e-9 {
		t.Fatalf("gain = %f", got)
	}
}

func TestGainValuesVisitMinutes(t *testing.T) {
	p := DefaultScoreParams()
	hour := p.gain(1, time.Hour, 0, 0, 0)
	half := p.gain(1, 30*time.Minute, 0, 0, 0)
	if hour != 0.5*60-5 || half != 0.5*30-5 {
		t.Fatalf("hour %v, half hour %v", hour, half)
	}
}

func TestGainOutweighsAWalkForAMatchingVisit(t *testing.T) {
	p := DefaultScoreParams()
	walk := p.TransitWeight * 17 // about one kilometre on foot
	if g := p.gain(1.5, time.Hour, 0, walk, 0); g <= 0 {
		t.Fatalf("an hour at an archetype place after a 1 km walk scores %v", g)
	}
	if g := p.gain(0.15, time.Hour, 0, walk, 0); g >= 0 {
		t.Fatalf("an hour at an unmatched place after a 1 km walk scores %v", g)
	}
}

func TestGainChargesRepeatedCategory(t *testing.T) {
	p := DefaultScoreParams()
	if first, second := p.gain(1, time.Hour, 0, 0, 0), p.gain(1, time.Hour, 0, 0, 1); first-second != 2*p.CategoryWeight {
		t.Fatalf("first %v, second %v", first, second)
	}
}
