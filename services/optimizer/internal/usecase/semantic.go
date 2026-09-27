package usecase

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"

	"github.com/andres1m/impuls-goroda/pkg/ai"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/semantic"
)

var semanticFallbacks = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "planner_semantic_fallback_total",
	Help: "Requests with free-text wishes planned by interests alone, by the reason the wishes could not be applied.",
}, []string{"reason"})

// SemanticMatcher finds the catalog entities of a city closest in meaning to free text.
type SemanticMatcher interface {
	Match(ctx context.Context, city, query string) (semantic.Matches, error)
}

// WithSemantic narrows the candidates to the free-text wishes of a request. A matcher that
// fails or takes longer than timeout leaves the request planned by its interests alone.
func WithSemantic(m SemanticMatcher, timeout time.Duration) Option {
	return func(p *Planner) {
		p.semantic = m
		p.semanticTimeout = timeout
	}
}

// semanticPool fails only when the request itself is cancelled; any other failure is reported
// by a warning, and the whole pool is planned.
func (p *Planner) semanticPool(ctx context.Context, req domain.OptimizeRequest, pool []domain.Candidate) ([]domain.Candidate, *domain.Warning, error) {
	if req.Constraints.SemanticQuery == "" {
		return pool, nil, nil
	}
	narrowed, reason, err := p.narrow(ctx, req, pool)
	if reason == "" {
		return narrowed, nil, nil
	}
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	semanticFallbacks.WithLabelValues(reason).Inc()
	p.log.Warn("free-text wishes are not applied", zap.String("city", req.City), zap.String("reason", reason), zap.Error(err))
	return pool, &domain.Warning{
		Code: "SEMANTIC_UNAVAILABLE", Scope: domain.ScopeRoute,
		Message: "Free-text wishes could not be applied (" + reason + "); the routes follow the selected interests",
	}, nil
}

func (p *Planner) narrow(ctx context.Context, req domain.OptimizeRequest, pool []domain.Candidate) ([]domain.Candidate, string, error) {
	if p.semantic == nil {
		return nil, "disabled", nil
	}
	if p.semanticTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.semanticTimeout)
		defer cancel()
	}
	matches, err := p.semantic.Match(ctx, req.City, req.Constraints.SemanticQuery)
	if err != nil {
		return nil, fallbackReason(err), err
	}
	narrowed := slices.DeleteFunc(slices.Clone(pool), func(c domain.Candidate) bool { return !matches.Contains(c) })
	if len(narrowed) == 0 {
		return nil, "no_match", nil
	}
	return narrowed, "", nil
}

func fallbackReason(err error) string {
	switch {
	case errors.Is(err, ai.ErrNotImplemented):
		return "not_implemented"
	case errors.Is(err, ai.ErrNotConfigured):
		return "not_configured"
	case errors.Is(err, semantic.ErrNoVectors):
		return "no_vectors"
	case errors.Is(err, ai.ErrDimensions):
		return "dimensions"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "error"
	}
}
