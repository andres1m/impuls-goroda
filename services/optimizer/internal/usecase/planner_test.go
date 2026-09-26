package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func TestPlannerIsNotImplemented(t *testing.T) {
	p := NewPlanner()
	if _, err := p.Optimize(context.Background(), domain.OptimizeRequest{}); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("Optimize() error = %v, want ErrNotImplemented", err)
	}
	if _, err := p.Recompute(context.Background(), domain.RecomputeRequest{}); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("Recompute() error = %v, want ErrNotImplemented", err)
	}
}
