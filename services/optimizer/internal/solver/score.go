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
func (p ScoreParams) gain(utility float64, wait time.Duration, travel float64, categoryCount int) float64 {
	return utility -
		p.WaitWeight*wait.Minutes() -
		travel -
		p.CategoryWeight*float64(2*categoryCount+1)
}

// Scenic rates the surroundings of a walk between two points from 0, bare, to 1, fully scenic.
// It says nothing about whether the way can be walked.
type Scenic interface {
	Score(from, to domain.Coordinate) float64
}

// travelPenalty is what a leg costs; walking through scenic surroundings costs less, never nothing.
func (r searchRun) travelPenalty(from, to domain.Coordinate, leg domain.TransitEstimate) float64 {
	minutes := leg.Duration.Minutes()
	penalty := r.score.TransitWeight * minutes
	if leg.Mode == domain.MovementWalk && r.problem.Scenic != nil && r.score.ScenicWeight > 0 {
		penalty -= r.score.ScenicWeight * min(max(r.problem.Scenic.Score(from, to), 0), 1) * minutes
	}
	return penalty
}

// finishPenalty charges the leg from position to the destination, which is part of the route's travel.
func (r searchRun) finishPenalty(position domain.Coordinate, finish *domain.TransitEstimate) float64 {
	if finish == nil {
		return 0
	}
	return r.travelPenalty(position, *r.problem.Destination, *finish)
}
