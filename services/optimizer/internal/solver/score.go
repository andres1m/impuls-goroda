package solver

import (
	"math"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func (p ScoreParams) affinity(user, visit domain.InterestMask, archetype domain.Archetype) float64 {
	bonus := 1.0
	if visit&archetype.Mask() != 0 {
		bonus = p.ArchetypeBonus
	}
	if user.IsEmpty() {
		return p.AffinityBase * bonus
	}
	if k := user.Matches(visit); k > 0 {
		return (p.AffinityBase + p.AffinityScale*math.Log2(float64(k+1))) * bonus
	}
	return p.NoMatchFactor * bonus
}

// gain is the score added by one visit; the quadratic category penalty grows by 2n+1 when n visits of the category exist.
func (p ScoreParams) gain(utility float64, wait, transit time.Duration, categoryCount int) float64 {
	return utility -
		p.WaitWeight*wait.Minutes() -
		p.TransitWeight*transit.Minutes() -
		p.CategoryWeight*float64(2*categoryCount+1)
}
