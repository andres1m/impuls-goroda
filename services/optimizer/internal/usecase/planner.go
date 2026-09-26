package usecase

import (
	"context"
	"errors"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

var (
	ErrNotImplemented  = errors.New("route computation is not implemented")
	ErrCatalogNotReady = errors.New("city catalog is not ready")
	ErrStaleCatalog    = errors.New("catalog is older than required")
	ErrUnavailable     = errors.New("catalog storage is unavailable")
	ErrOverloaded      = errors.New("too many concurrent computations")
)

// Planner computes routes. Until the solver exists it refuses every request honestly
// instead of inventing a plan.
type Planner struct{}

func NewPlanner() *Planner {
	return &Planner{}
}

func (*Planner) Optimize(context.Context, domain.OptimizeRequest) (domain.OptimizeResult, error) {
	return domain.OptimizeResult{}, ErrNotImplemented
}

func (*Planner) Recompute(context.Context, domain.RecomputeRequest) (domain.RecomputeResult, error) {
	return domain.RecomputeResult{}, ErrNotImplemented
}
