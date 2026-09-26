package solver

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"golang.org/x/sync/errgroup"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// Solver runs a bounded-width beam search over a candidate pool. It keeps the best partial
// routes of every layer and never proves a global optimum.
type Solver struct {
	cfg       Config
	score     ScoreParams
	transit   Transit
	placement Placement
}

func New(cfg Config, score ScoreParams, transit Transit, placement Placement) (*Solver, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := score.Validate(); err != nil {
		return nil, err
	}
	if transit == nil || placement == nil {
		return nil, errors.New("transit and placement rules are required")
	}
	return &Solver{cfg: cfg, score: score, transit: transit, placement: placement}, nil
}

// Search returns up to BeamWidth best routes with at least one visit, best first.
// The routes reference the pool's candidates, which must not be modified afterwards.
func (s *Solver) Search(ctx context.Context, p Problem, pool []domain.Candidate) ([]*domain.Branch, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	for i := range pool {
		if err := pool[i].Validate(); err != nil {
			return nil, fmt.Errorf("candidate %d: %w", i, err)
		}
	}
	run := searchRun{Solver: s, problem: p, pool: pool, utilities: make([]float64, len(pool))}
	for i := range pool {
		run.utilities[i] = pool[i].BaseScore * s.score.affinity(p.Interests, pool[i].InterestMask(), p.Archetype)
	}

	beam := []*domain.Branch{domain.NewBranch(p.Origin, p.Start, p.Currency)}
	var best []*domain.Branch
	for len(beam) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		children, err := run.expandAll(ctx, beam)
		if err != nil {
			return nil, err
		}
		beam = top(children, s.cfg.BeamWidth)
		best = top(append(best, beam...), s.cfg.BeamWidth)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return best, nil
}

type searchRun struct {
	*Solver
	problem   Problem
	pool      []domain.Candidate
	utilities []float64
}

func (r searchRun) expandAll(ctx context.Context, beam []*domain.Branch) ([]*domain.Branch, error) {
	perParent := make([][]*domain.Branch, len(beam))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(r.cfg.Parallelism)
	for i, parent := range beam {
		g.Go(func() error {
			if err := gctx.Err(); err != nil {
				return err
			}
			perParent[i] = r.expand(parent)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return slices.Concat(perParent...), nil
}

func (r searchRun) expand(parent *domain.Branch) []*domain.Branch {
	var children []*domain.Branch
	for i := range r.pool {
		c := &r.pool[i]
		if _, visited := parent.VisitedPlaces[c.Place.ID]; visited {
			continue
		}
		leg, ok := r.transit.Estimate(parent.Position, c.Place.Location, parent.Now, r.problem.Modes)
		if !ok {
			continue
		}
		arrival := parent.Now.Add(leg.Duration)
		start, end, ok := r.placement.Place(c, arrival, r.problem)
		if !ok {
			continue
		}
		visit := domain.SearchVisit{Candidate: c, Transit: leg, ArrivalAt: arrival, StartAt: start, EndAt: end}
		children = append(children, r.extend(parent, visit, r.utilities[i]))
	}
	return children
}

func (r searchRun) extend(parent *domain.Branch, visit domain.SearchVisit, utility float64) *domain.Branch {
	child := parent.Clone()
	c := visit.Candidate
	category := c.Category()
	child.Score += r.score.gain(utility, visit.StartAt.Sub(visit.ArrivalAt), visit.Transit.Duration, child.CountCategory(category))
	child.AddCategory(category)
	child.Visits = append(child.Visits, visit)
	child.VisitedPlaces[c.Place.ID] = struct{}{}
	if c.Session != nil {
		child.UsedSessions[c.Session.ID] = struct{}{}
	}
	child.Position = c.Place.Location
	child.Now = visit.EndAt
	return child
}

func top(branches []*domain.Branch, n int) []*domain.Branch {
	slices.SortFunc(branches, compareBranches)
	return branches[:min(n, len(branches))]
}

// compareBranches breaks score ties by the visit sequence, so the result does not
// depend on how goroutines were scheduled.
func compareBranches(a, b *domain.Branch) int {
	if c := cmp.Compare(b.Score, a.Score); c != 0 {
		return c
	}
	return slices.CompareFunc(a.Visits, b.Visits, compareVisits)
}

func compareVisits(a, b domain.SearchVisit) int {
	if c := bytes.Compare(a.Candidate.Place.ID[:], b.Candidate.Place.ID[:]); c != 0 {
		return c
	}
	return bytes.Compare(sessionKey(a), sessionKey(b))
}

func sessionKey(v domain.SearchVisit) []byte {
	if v.Candidate.Session == nil {
		return nil
	}
	return v.Candidate.Session.ID[:]
}
