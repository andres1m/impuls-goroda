package usecase

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/semantic"
)

// BenchmarkOptimizeSemantic measures the planner's own share of the semantic path with an
// instant matcher; the embedding provider's latency is not part of it.
func BenchmarkOptimizeSemantic(b *testing.B) {
	var candidates []domain.Candidate
	var matches []semantic.Match
	for i := byte(1); i <= 60; i++ {
		category := []domain.Category{domain.CategoryCulture, domain.CategoryWalk, domain.CategorySport}[i%3]
		candidates = append(candidates, place(i, category, north(origin, float64(i)*90)))
		if i%2 == 0 {
			matches = append(matches, placeMatch(i))
		}
	}
	p, err := NewPlanner(config(), fakeSource{candidates: candidates}, estimated(), zap.NewNop(),
		WithSemantic(&fakeMatcher{matches: matches}, time.Second))
	if err != nil {
		b.Fatal(err)
	}
	req := request()
	req.Constraints.SemanticQuery = "прогулка и музей"
	b.ResetTimer()
	for b.Loop() {
		if _, err := p.Optimize(context.Background(), req); err != nil {
			b.Fatal(err)
		}
	}
}
