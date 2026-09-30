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
func (s *Solver) Search(ctx context.Context, p *Problem, pool []domain.Candidate) ([]*domain.Branch, error) {
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
			return nil, fmt.Errorf("search canceled: %w", err)
		}
		children, err := run.expandAll(ctx, beam)
		if err != nil {
			return nil, err
		}
		beam = top(children, s.cfg.BeamWidth)
		best = top(
			append(
				best,
				slices.DeleteFunc(slices.Clone(beam), func(b *domain.Branch) bool { return !run.complete(b) })...),
			s.cfg.BeamWidth,
		)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("search canceled: %w", err)
	}
	return best, nil
}

// newRun prepares one run over the pool with the problem's anchors in it.
func (s *Solver) newRun(p *Problem, pool []domain.Candidate) (*searchRun, error) {
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
			return nil, fmt.Errorf("candidate %d: %w", i, err)
		}
	}
	pool = p.Load.trim(pool, isAnchor)
	run := &searchRun{
		Solver:          s,
		problem:         *p,
		pool:            pool,
		anchors:         anchors,
		isAnchor:        isAnchor,
		fixedAnchors:    fixed,
		flexibleAnchors: flexible,
		utilities: make(
			[]float64,
			len(pool),
		),
		quotes:    make([]pricing.Quote, len(pool)),
		priced:    make([]bool, len(pool)),
		lunchOnly: make([]bool, len(pool)),
		visitNorm: p.Load.visitNorm(p.End.Sub(p.Start)),
	}
	for i := range pool {
		run.utilities[i] = pool[i].BaseScore * s.score.affinity(p.Interests, pool[i].InterestMask(), p.Archetype)
		run.quotes[i], run.priced[i] = p.Pricing.Quote(&pool[i])
		if p.Lunch != nil && !isAnchor[i] && lunchVenue(&pool[i], p.Lunch.Duration) {
			mealPolicy := p.Pricing
			mealPolicy.PushkinCardOnly = false
			mealPolicy.AcceptUnknownPrice = true
			run.quotes[i], run.priced[i] = mealPolicy.Quote(&pool[i])
			run.lunchVenues = append(run.lunchVenues, i)
			run.lunchOnly[i] = true
		}
		// Assumed opening hours are good enough to suggest lunch, never to plan a visit on.
		if pool[i].Window.HoursUnknown {
			run.lunchOnly[i] = true
		}
	}
	return run, nil
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
	// Visits the pace allows before each further one is charged; zero sets no norm.
	visitNorm int
	// False when the route's money constraints exclude the candidate.
	priced []bool
	// Pool indices of the places that can host the problem's lunch; they are visited for lunch only,
	// so a route never eats there earlier and then reports no place for lunch.
	lunchVenues []int
	lunchOnly   []bool
}

func (r *searchRun) root() *domain.Branch {
	root := domain.NewBranch(r.problem.Origin, r.problem.Start, r.problem.Pricing.Currency)
	for _, id := range r.problem.Visited {
		root.VisitedPlaces[id] = struct{}{}
	}
	return root
}

func (r *searchRun) expandAll(ctx context.Context, beam []*domain.Branch) ([]*domain.Branch, error) {
	perParent := make([][]*domain.Branch, len(beam))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(r.cfg.Parallelism)
	for i, parent := range beam {
		g.Go(func() error {
			if err := gctx.Err(); err != nil {
				return fmt.Errorf("expand canceled: %w", err)
			}
			perParent[i] = r.expand(parent)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("expand beam: %w", err)
	}
	return slices.Concat(perParent...), nil
}

func (r *searchRun) expand(parent *domain.Branch) []*domain.Branch {
	var children []*domain.Branch
	rest, due := r.restDue(parent)
	// A rest the day has no room for is simply left out; one that fits costs something to skip.
	fits := due && r.restFits(parent, rest)
	for i := range r.pool {
		if child, ok := r.expandCandidate(parent, i, rest, fits); ok {
			children = append(children, child)
		}
	}
	return append(children, r.lunches(parent)...)
}

func (r *searchRun) expandCandidate(parent *domain.Branch, i int, rest domain.Rest, fits bool) (*domain.Branch, bool) {
	c := &r.pool[i]
	_, visited := parent.VisitedPlaces[c.Place.ID]
	// An anchor is a commitment, so it stays even at a place visited earlier today.
	if r.isAnchor[i] {
		visited = done(parent, c)
	}
	if visited || !r.priced[i] || r.lunchOnly[i] {
		return nil, false
	}
	if !r.problem.Pricing.Fits(parent.KnownCost, r.quotes[i]) {
		return nil, false
	}
	if fits {
		if child, ok := r.visit(parent, i, rest.EndAt); ok {
			child.Rests = append(child.Rests, rest)
			return child, true
		}
	}
	// The rest is soft: without room for it the visit still goes ahead.
	child, ok := r.visit(parent, i, parent.Now)
	if !ok {
		return nil, false
	}
	if fits {
		child.Score -= restSkipWeight
	}
	return child, true
}

// visit extends the branch with the pool's candidate i, leaving the current place at departAt.
func (r *searchRun) visit(parent *domain.Branch, i int, departAt time.Time) (*domain.Branch, bool) {
	c := &r.pool[i]
	leg, ok := r.transit.Estimate(parent.Position, c.Place.Location, departAt, r.problem.Modes)
	if !ok {
		return nil, false
	}
	arrival := departAt.Add(leg.Duration)
	slot, finish, ok := r.place(c, arrival)
	if !ok {
		return nil, false
	}
	visit := domain.SearchVisit{
		Candidate: c,
		Transit:   leg,
		ArrivalAt: arrival,
		Buffer:    slot.Buffer,
		StartAt:   slot.StartAt,
		EndAt:     slot.EndAt,
	}
	child := r.extend(parent, &visit, finish, r.utilities[i], r.quotes[i], r.isAnchor[i])
	if !r.anchorsReachable(child) {
		// A shorter stay in an open window may still leave time for the commitments ahead.
		if c.Window.Kind != domain.WindowContinuous {
			return nil, false
		}
		short, shortFinish, shortOK := r.placeBy(c, arrival, slot.StartAt.Add(c.Window.MinDuration))
		if !shortOK {
			return nil, false
		}
		visit.Buffer, visit.StartAt, visit.EndAt = short.Buffer, short.StartAt, short.EndAt
		if child = r.extend(
			parent,
			&visit,
			shortFinish,
			r.utilities[i],
			r.quotes[i],
			r.isAnchor[i],
		); !r.anchorsReachable(
			child,
		) {
			return nil, false
		}
	}
	if !r.lunchStillFits(child) {
		return nil, false
	}
	return child, true
}

// restFits tells whether the branch can pause for the rest and still keep its commitments, its lunch
// and the way to the destination.
func (r *searchRun) restFits(parent *domain.Branch, rest domain.Rest) bool {
	// The checks only read the branch, so a shallow copy is enough.
	rested := *parent
	rested.Now = rest.EndAt
	if !r.anchorsReachable(&rested) || !r.lunchStillFits(&rested) {
		return false
	}
	if r.problem.Destination == nil {
		return !rest.EndAt.After(r.problem.End)
	}
	finish, ok := r.transit.Estimate(parent.Position, *r.problem.Destination, rest.EndAt, r.problem.Modes)
	return ok && !rest.EndAt.Add(finish.Duration).After(r.problem.End)
}

// restDue is the rest the pace asks for before the next visit, if one is due now.
func (r *searchRun) restDue(b *domain.Branch) (domain.Rest, bool) {
	l := r.problem.Load
	n := len(b.Visits)
	since := r.problem.VisitsSinceRest + n
	if l.RestEvery == 0 || l.Rest == 0 || since == 0 || since%l.RestEvery != 0 ||
		slices.ContainsFunc(b.Rests, func(x domain.Rest) bool { return x.At == n }) {
		return domain.Rest{}, false
	}
	return domain.Rest{At: n, StartAt: b.Now, EndAt: b.Now.Add(l.Rest)}, true
}

// place fits the visit into the day so that the destination, if any, is still reachable in time.
func (r *searchRun) place(c *domain.Candidate, arrival time.Time) (Slot, *domain.TransitEstimate, bool) {
	return r.placeBy(c, arrival, r.problem.End)
}

// placeBy is place with the visit ending no later than deadline.
func (r *searchRun) placeBy(c *domain.Candidate, arrival, deadline time.Time) (Slot, *domain.TransitEstimate, bool) {
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

func (r *searchRun) finish(c *domain.Candidate, departAt time.Time) (domain.TransitEstimate, bool) {
	return r.transit.Estimate(c.Place.Location, *r.problem.Destination, departAt, r.problem.Modes)
}

// extend adds the visit. Every visit but a lunch venue counts towards the pace's norm; an exempt one, a
// commitment or a lunch, is never charged for going over it.
func (r *searchRun) extend(
	parent *domain.Branch,
	visit *domain.SearchVisit,
	finish *domain.TransitEstimate,
	utility float64,
	quote pricing.Quote,
	exempt bool,
) *domain.Branch {
	child := parent.Clone()
	c := visit.Candidate
	category := c.Category()
	wait := visit.StartAt.Sub(visit.ArrivalAt) - visit.Buffer
	child.Score += r.score.gain(
		utility,
		visit.EndAt.Sub(visit.StartAt),
		wait,
		r.travelPenalty(parent.Position, c.Place.Location, &visit.Transit),
		child.CountCategory(category),
	)
	child.Score += r.finishPenalty(parent.Position, parent.Finish) - r.finishPenalty(c.Place.Location, finish)
	child.Finish = finish
	if visit.Transit.Mode == domain.MovementWalk {
		child.WalkMinutes += visit.Transit.Duration.Minutes()
	}
	child.Score += r.walkPenalty(parent) - r.walkPenalty(child)
	counted := len(child.Visits)
	if child.Lunch != nil && child.Lunch.Venue {
		counted--
	}
	if r.visitNorm > 0 && counted >= r.visitNorm && !exempt {
		child.Score -= r.problem.Load.OverVisitWeight
	}
	if upper, known := quote.Price.UpperBound(); known {
		child.KnownCost.AmountMinor = saturatingAdd(child.KnownCost.AmountMinor, upper.AmountMinor)
	} else {
		child.UnknownCost = true
	}
	child.AddCategory(category)
	child.Visits = append(child.Visits, *visit)
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
	if c := compareVisitSlices(a.Visits, b.Visits); c != 0 {
		return c
	}
	if c := compareLunches(a.Lunch, b.Lunch); c != 0 {
		return c
	}
	return cmp.Compare(len(a.Rests), len(b.Rests))
}

func compareVisitSlices(a, b []domain.SearchVisit) int {
	limit := min(len(a), len(b))
	for i := range limit {
		if c := compareVisits(&a[i], &b[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a), len(b))
}

func compareVisits(a, b *domain.SearchVisit) int {
	if c := bytes.Compare(a.Candidate.Place.ID[:], b.Candidate.Place.ID[:]); c != 0 {
		return c
	}
	return bytes.Compare(sessionKey(a), sessionKey(b))
}

func sessionKey(v *domain.SearchVisit) []byte {
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
