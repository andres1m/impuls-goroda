package solver

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/pricing"
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
	run := searchRun{
		Solver: s, problem: p, pool: pool,
		utilities: make([]float64, len(pool)), quotes: make([]pricing.Quote, len(pool)), priced: make([]bool, len(pool)),
	}
	for i := range pool {
		run.utilities[i] = pool[i].BaseScore * s.score.affinity(p.Interests, pool[i].InterestMask(), p.Archetype)
		run.quotes[i], run.priced[i] = p.Pricing.Quote(&pool[i])
	}

	beam := []*domain.Branch{domain.NewBranch(p.Origin, p.Start, p.Pricing.Currency)}
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
	quotes    []pricing.Quote
	// False when the route's money constraints exclude the candidate.
	priced []bool
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
		if _, visited := parent.VisitedPlaces[c.Place.ID]; visited || !r.priced[i] {
			continue
		}
		if !r.problem.Pricing.Fits(parent.KnownCost, r.quotes[i]) {
			continue
		}
		leg, ok := r.transit.Estimate(parent.Position, c.Place.Location, parent.Now, r.problem.Modes)
		if !ok {
			continue
		}
		arrival := parent.Now.Add(leg.Duration)
		slot, finish, ok := r.place(c, arrival)
		if !ok {
			continue
		}
		visit := domain.SearchVisit{Candidate: c, Transit: leg, ArrivalAt: arrival, Buffer: slot.Buffer, StartAt: slot.StartAt, EndAt: slot.EndAt}
		children = append(children, r.extend(parent, visit, finish, r.utilities[i], r.quotes[i]))
	}
	return children
}

// place fits the visit into the day so that the destination, if any, is still reachable in time.
func (r searchRun) place(c *domain.Candidate, arrival time.Time) (Slot, *domain.TransitEstimate, bool) {
	end := r.problem.End
	slot, ok := r.placement.Place(c, arrival, end)
	if !ok || r.problem.Destination == nil {
		return slot, nil, ok
	}
	finish, ok := r.finish(c, slot.EndAt)
	if !ok {
		return Slot{}, nil, false
	}
	if !slot.EndAt.Add(finish.Duration).After(end) {
		return slot, &finish, true
	}
	// A shorter visit may leave enough time; the leg is re-estimated because it depends on when the user leaves.
	if slot, ok = r.placement.Place(c, arrival, end.Add(-finish.Duration)); !ok {
		return Slot{}, nil, false
	}
	if finish, ok = r.finish(c, slot.EndAt); !ok || slot.EndAt.Add(finish.Duration).After(end) {
		return Slot{}, nil, false
	}
	return slot, &finish, true
}

func (r searchRun) finish(c *domain.Candidate, departAt time.Time) (domain.TransitEstimate, bool) {
	return r.transit.Estimate(c.Place.Location, *r.problem.Destination, departAt, r.problem.Modes)
}

func (r searchRun) extend(parent *domain.Branch, visit domain.SearchVisit, finish *domain.TransitEstimate, utility float64, quote pricing.Quote) *domain.Branch {
	child := parent.Clone()
	c := visit.Candidate
	category := c.Category()
	wait := visit.StartAt.Sub(visit.ArrivalAt) - visit.Buffer
	child.Score += r.score.gain(utility, wait, visit.Transit.Duration, child.CountCategory(category))
	child.Score += r.score.finishPenalty(parent.Finish) - r.score.finishPenalty(finish)
	child.Finish = finish
	if upper, known := quote.Price.UpperBound(); known {
		child.KnownCost.AmountMinor = saturatingAdd(child.KnownCost.AmountMinor, upper.AmountMinor)
	} else {
		child.UnknownCost = true
	}
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

// saturatingAdd adds non-negative amounts. A capped sum only matters to a strict budget,
// which rejects it anyway.
func saturatingAdd(a, b int64) int64 {
	if b > math.MaxInt64-a {
		return math.MaxInt64
	}
	return a + b
}
