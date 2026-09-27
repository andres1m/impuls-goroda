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

// Search returns up to BeamWidth best routes with at least one visit, best first. Every route
// contains all anchors; when no route can, the result is empty.
// The routes reference the pool's candidates, which must not be modified afterwards.
func (s *Solver) Search(ctx context.Context, p Problem, pool []domain.Candidate) ([]*domain.Branch, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	run, err := s.newRun(p, pool)
	if err != nil {
		return nil, err
	}
	beam := []*domain.Branch{run.root()}
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
		best = top(append(best, slices.DeleteFunc(slices.Clone(beam), func(b *domain.Branch) bool { return !run.complete(b) })...), s.cfg.BeamWidth)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return best, nil
}

// newRun prepares one run over the pool with the problem's anchors in it.
func (s *Solver) newRun(p Problem, pool []domain.Candidate) (searchRun, error) {
	pool, anchors := mergeAnchors(pool, p.Anchors)
	isAnchor := make([]bool, len(pool))
	var fixed, flexible []int
	for _, i := range anchors {
		isAnchor[i] = true
		if fixedTime(&pool[i]) {
			fixed = append(fixed, i)
		} else {
			flexible = append(flexible, i)
		}
	}
	slices.SortStableFunc(fixed, func(a, b int) int { return pool[a].Window.Start.Compare(pool[b].Window.Start) })
	for i := range pool {
		if err := pool[i].Validate(); err != nil {
			return searchRun{}, fmt.Errorf("candidate %d: %w", i, err)
		}
	}
	run := searchRun{
		Solver: s, problem: p, pool: pool, anchors: anchors, isAnchor: isAnchor, fixedAnchors: fixed, flexibleAnchors: flexible,
		utilities: make([]float64, len(pool)), quotes: make([]pricing.Quote, len(pool)), priced: make([]bool, len(pool)),
		lunchOnly: make([]bool, len(pool)),
	}
	for i := range pool {
		run.utilities[i] = pool[i].BaseScore * s.score.affinity(p.Interests, pool[i].InterestMask(), p.Archetype)
		run.quotes[i], run.priced[i] = p.Pricing.Quote(&pool[i])
		if p.Lunch != nil && !isAnchor[i] && lunchVenue(&pool[i], p.Lunch.Duration) {
			run.lunchVenues = append(run.lunchVenues, i)
			run.lunchOnly[i] = true
		}
	}
	return run, nil
}

func (r searchRun) root() *domain.Branch {
	root := domain.NewBranch(r.problem.Origin, r.problem.Start, r.problem.Pricing.Currency)
	for _, id := range r.problem.Visited {
		root.VisitedPlaces[id] = struct{}{}
	}
	return root
}

type searchRun struct {
	*Solver
	problem Problem
	pool    []domain.Candidate
	// Pool indices of the anchors; the fixed-time ones in the order they start.
	anchors         []int
	isAnchor        []bool
	fixedAnchors    []int
	flexibleAnchors []int
	utilities       []float64
	quotes          []pricing.Quote
	// False when the route's money constraints exclude the candidate.
	priced []bool
	// Pool indices of the places that can host the problem's lunch; they are visited for lunch only,
	// so a route never eats there earlier and then reports no place for lunch.
	lunchVenues []int
	lunchOnly   []bool
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
		_, visited := parent.VisitedPlaces[c.Place.ID]
		// An anchor is a commitment, so it stays even at a place visited earlier today.
		if r.isAnchor[i] {
			visited = done(parent, c)
		}
		if visited || !r.priced[i] || r.lunchOnly[i] {
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
		child := r.extend(parent, visit, finish, r.utilities[i], r.quotes[i])
		if !r.anchorsReachable(child) {
			// A shorter stay in an open window may still leave time for the commitments ahead.
			if c.Window.Kind != domain.WindowContinuous {
				continue
			}
			short, shortFinish, ok := r.placeBy(c, arrival, slot.StartAt.Add(c.Window.MinDuration))
			if !ok {
				continue
			}
			visit.Buffer, visit.StartAt, visit.EndAt = short.Buffer, short.StartAt, short.EndAt
			if child = r.extend(parent, visit, shortFinish, r.utilities[i], r.quotes[i]); !r.anchorsReachable(child) {
				continue
			}
		}
		if !r.lunchStillFits(child) {
			continue
		}
		children = append(children, child)
	}
	return append(children, r.lunches(parent)...)
}

// place fits the visit into the day so that the destination, if any, is still reachable in time.
func (r searchRun) place(c *domain.Candidate, arrival time.Time) (Slot, *domain.TransitEstimate, bool) {
	return r.placeBy(c, arrival, r.problem.End)
}

// placeBy is place with the visit ending no later than deadline.
func (r searchRun) placeBy(c *domain.Candidate, arrival, deadline time.Time) (Slot, *domain.TransitEstimate, bool) {
	end := r.problem.End
	slot, ok := r.placement.Place(c, arrival, deadline)
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
	if slot, ok = r.placement.Place(c, arrival, earlier(deadline, end.Add(-finish.Duration))); !ok {
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
	if c := slices.CompareFunc(a.Visits, b.Visits, compareVisits); c != 0 {
		return c
	}
	return compareLunches(a.Lunch, b.Lunch)
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

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
