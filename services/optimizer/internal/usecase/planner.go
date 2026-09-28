package usecase

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/pricing"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/solver"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/validation"
)

var (
	ErrNotImplemented  = errors.New("route computation is not implemented")
	ErrCatalogNotReady = errors.New("city catalog is not ready")
	ErrStaleCatalog    = errors.New("catalog is older than required")
	ErrUnavailable     = errors.New("catalog storage is unavailable")
	ErrOverloaded      = errors.New("too many concurrent computations")
	ErrInvalidRequest  = errors.New("invalid request")
)

var rejectedPlans = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "planner_rejected_plans_total",
	Help: "Plans the independent validation refused, by the first rule they broke.",
}, []string{"code"})

var scenicUnavailable = promauto.NewCounter(prometheus.CounterOpts{
	Name: "planner_scenic_unavailable_total",
	Help: "Searches planned without scenic scores because they could not be prepared.",
})

// CandidateSource supplies the catalog candidates of a request and how fresh they are.
type CandidateSource interface {
	Candidates(ctx context.Context, req domain.OptimizeRequest) ([]domain.Candidate, domain.DataFreshness, error)
}

// TransitProvider prepares travel between the points of one search; degraded reports a fallback
// that cannot see obstacles.
type TransitProvider interface {
	Transit(ctx context.Context, city string, points []domain.Coordinate, modes []domain.MovementMode) (solver.Transit, bool, error)
}

// CatalogNotReady is the source until the catalog can be read: it refuses instead of inventing places.
type CatalogNotReady struct{}

func (CatalogNotReady) Candidates(context.Context, domain.OptimizeRequest) ([]domain.Candidate, domain.DataFreshness, error) {
	return nil, domain.DataFreshness{}, ErrCatalogNotReady
}

// ScenicProvider rates walks in a city as its catalog stood at the revision.
type ScenicProvider interface {
	Scenic(ctx context.Context, city string, revision domain.CatalogRevision) (solver.Scenic, error)
}

type Config struct {
	Currency    string `yaml:"currency"`
	BeamWidth   int    `yaml:"beam-width"`
	Parallelism int    `yaml:"parallelism"`
	// Per city; a city without a weight is planned without scenic scores.
	ScenicWeights map[string]float64 `yaml:"scenic-weights"`
}

type Option func(*Planner)

// WithScenic lets walks through scenic surroundings cost less in the cities that have a scenic weight.
func WithScenic(scenic ScenicProvider) Option {
	return func(p *Planner) { p.scenic = scenic }
}

// Planner builds routes: one search per archetype, each result assembled into a plan and
// released only after the independent validation accepts it.
type Planner struct {
	cfg     Config
	source  CandidateSource
	transit TransitProvider
	scenic  ScenicProvider
	log     *zap.Logger
	newID   func() domain.VisitID

	semantic        SemanticMatcher
	semanticTimeout time.Duration
}

func NewPlanner(cfg Config, source CandidateSource, transit TransitProvider, log *zap.Logger, opts ...Option) (*Planner, error) {
	if err := (solver.Config{BeamWidth: cfg.BeamWidth, Parallelism: cfg.Parallelism}).Validate(); err != nil {
		return nil, err
	}
	for city, weight := range cfg.ScenicWeights {
		if err := scoreParams(weight).Validate(); err != nil {
			return nil, fmt.Errorf("scenic weight of %s: %w", city, err)
		}
	}
	if err := (domain.Money{Currency: cfg.Currency}).Validate(); err != nil {
		return nil, err
	}
	if source == nil || transit == nil || log == nil {
		return nil, errors.New("planner needs a candidate source, a transit provider and a logger")
	}
	p := &Planner{cfg: cfg, source: source, transit: transit, log: log, newID: randomVisitID}
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

func scoreParams(scenicWeight float64) solver.ScoreParams {
	params := solver.DefaultScoreParams()
	params.ScenicWeight = scenicWeight
	return params
}

// scenicFor prepares the city's scenic scores; being a preference, a failure only leaves them out.
func (p *Planner) scenicFor(ctx context.Context, city string, revision domain.CatalogRevision, weight float64) solver.Scenic {
	if p.scenic == nil || weight == 0 {
		return nil
	}
	scenic, err := p.scenic.Scenic(ctx, city, revision)
	if err != nil {
		scenicUnavailable.Inc()
		p.log.Warn("planning without scenic scores", zap.String("city", city), zap.Error(err))
		return nil
	}
	return scenic
}

func randomVisitID() domain.VisitID {
	var id domain.VisitID
	_, _ = rand.Read(id[:])
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	return id
}

var archetypes = []domain.Archetype{domain.ArchetypeUrbanAvantgarde, domain.ArchetypeHistoryHeritage, domain.ArchetypeActionSocial}

func (p *Planner) Optimize(ctx context.Context, req domain.OptimizeRequest) (domain.OptimizeResult, error) {
	started := time.Now()
	policy := pricing.PolicyFor(p.cfg.Currency, req.Constraints)
	if err := policy.Validate(); err != nil {
		return domain.OptimizeResult{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	candidates, data, err := p.source.Candidates(ctx, req)
	if err != nil {
		return domain.OptimizeResult{}, err
	}
	done := func(res domain.OptimizeResult) (domain.OptimizeResult, error) {
		res.Data = data
		res.ComputationTime = time.Since(started)
		return res, nil
	}

	anchors, conflicts := solver.AnchorsFor(req.Constraints.Obligations, candidates)
	if len(conflicts) > 0 {
		return done(domain.OptimizeResult{Status: domain.ResultConflict, Conflicts: conflicts})
	}
	pool, semanticWarning, err := p.semanticPool(ctx, req, admissible(candidates, req.Constraints))
	if err != nil {
		return domain.OptimizeResult{}, err
	}
	transit, degraded, err := p.transit.Transit(ctx, req.City, points(req, pool, anchors), req.Constraints.MovementModes)
	if err != nil {
		return domain.OptimizeResult{}, err
	}
	scenicWeight := p.cfg.ScenicWeights[req.City]
	s, err := solver.New(solver.Config{BeamWidth: p.cfg.BeamWidth, Parallelism: p.cfg.Parallelism}, scoreParams(scenicWeight), transit, solver.WindowPlacement{})
	if err != nil {
		return domain.OptimizeResult{}, err
	}
	problem := solver.Problem{
		Start: req.Start, End: req.End, Origin: req.Origin, Destination: req.Destination,
		Interests: req.Constraints.InterestMask, Modes: req.Constraints.MovementModes,
		Pricing: policy, Anchors: anchors, Archetype: archetypes[0],
		Scenic: p.scenicFor(ctx, req.City, data.CatalogRevision, scenicWeight),
		Load:   solver.ProfileFor(req.Constraints.LoadProfile),
	}
	if w := req.Constraints.LunchWindow; w != nil {
		slot := solver.LunchSlotFor(*w)
		problem.Lunch = &slot
	}
	if conflicts, err := s.Diagnose(ctx, problem); err != nil {
		return domain.OptimizeResult{}, err
	} else if len(conflicts) > 0 {
		return done(domain.OptimizeResult{Status: domain.ResultConflict, Conflicts: conflicts})
	}

	plans, lunchDropped, err := p.plans(ctx, req, policy, s, problem, pool, degraded, data)
	if err != nil {
		return domain.OptimizeResult{}, err
	}

	var warnings []domain.Warning
	if semanticWarning != nil {
		warnings = append(warnings, *semanticWarning)
	}
	if degraded {
		warnings = append(warnings, degradedWarning())
	}
	if lunchDropped && len(plans) > 0 {
		warnings = append(warnings, domain.Warning{
			Code: "LUNCH_NOT_RESERVED", Scope: domain.ScopeRoute,
			Message: "No route leaves room for the requested lunch; the routes are planned without it",
		})
	}
	if len(plans) == 0 {
		if len(anchors) > 0 {
			obligations := make([]domain.Obligation, len(anchors))
			for i, a := range anchors {
				obligations[i] = a.Obligation
			}
			return done(domain.OptimizeResult{Status: domain.ResultConflict, Warnings: warnings, Conflicts: []domain.Conflict{obligationsInfeasible(obligations)}})
		}
		warnings = append(warnings, noFeasibleWarning(pool))
		return done(domain.OptimizeResult{Status: domain.ResultNoFeasibleRoute, Warnings: warnings})
	}
	if len(plans) < len(archetypes) {
		warnings = append(warnings, fewerArchetypesWarning())
	}
	status := domain.ResultPartial
	if slices.ContainsFunc(plans, func(plan domain.Plan) bool { return plan.Result == domain.ResultReady }) {
		status = domain.ResultReady
	}
	return done(domain.OptimizeResult{Status: status, Routes: plans, Warnings: warnings})
}

type route struct {
	archetype domain.Archetype
	branch    *domain.Branch
}

type planned struct {
	branch *domain.Branch
	plan   domain.Plan
}

type archetypeBeam struct {
	archetype domain.Archetype
	branches  []*domain.Branch
	next      int
}

// plans also reports whether the requested lunch had to be left out because no route had room for it.
func (p *Planner) plans(ctx context.Context, req domain.OptimizeRequest, policy pricing.Policy, s *solver.Solver, problem solver.Problem, pool []domain.Candidate, degraded bool, data domain.DataFreshness) ([]domain.Plan, bool, error) {
	lunchDropped := false
	if problem.Lunch != nil && !lunchFits(problem) {
		problem.Lunch, lunchDropped = nil, true
	}
	plans, err := p.searchPlans(ctx, req, policy, s, problem, pool, degraded, data)
	if err != nil || len(plans) > 0 || problem.Lunch == nil {
		return plans, lunchDropped, err
	}
	problem.Lunch = nil
	plans, err = p.searchPlans(ctx, req, policy, s, problem, pool, degraded, data)
	return plans, true, err
}

func (p *Planner) searchPlans(ctx context.Context, req domain.OptimizeRequest, policy pricing.Policy, s *solver.Solver, problem solver.Problem, pool []domain.Candidate, degraded bool, data domain.DataFreshness) ([]domain.Plan, error) {
	beams, err := searchArchetypes(ctx, s, problem, pool)
	if err != nil || len(beams) == 0 {
		return nil, err
	}
	return p.selectPlans(req, policy, beams, degraded, data)
}

func lunchFits(p solver.Problem) bool {
	start := p.Start
	if p.Lunch.Start.After(start) {
		start = p.Lunch.Start
	}
	end := p.End
	if p.Lunch.End.Before(end) {
		end = p.Lunch.End
	}
	return end.Sub(start) >= p.Lunch.Duration
}

func searchArchetypes(ctx context.Context, s *solver.Solver, problem solver.Problem, pool []domain.Candidate) ([]archetypeBeam, error) {
	beams := make([]archetypeBeam, 0, len(archetypes))
	for _, a := range archetypes {
		problem.Archetype = a
		branches, err := s.Search(ctx, problem, pool)
		if err != nil {
			return nil, err
		}
		if len(branches) > 0 {
			beams = append(beams, archetypeBeam{archetype: a, branches: branches})
		}
	}
	return beams, nil
}

func (p *Planner) selectPlans(req domain.OptimizeRequest, policy pricing.Policy, beams []archetypeBeam, degraded bool, data domain.DataFreshness) ([]domain.Plan, error) {
	selected := make([]*planned, len(beams))
	for i := range beams {
		mask := beams[i].archetype.Mask()
		for beams[i].next < len(beams[i].branches) {
			cand := beams[i].branches[beams[i].next]
			beams[i].next++
			if !hasArchetypeMatch(cand, mask) {
				continue
			}
			prev := slices.IndexFunc(selected, func(other *planned) bool { return other != nil && sameVisits(other.branch, cand) })
			if prev >= 0 && cand.Score <= selected[prev].branch.Score {
				break
			}
			plan, ok, err := p.buildValid(req, policy, route{archetype: beams[i].archetype, branch: cand}, degraded, data)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			if prev >= 0 {
				selected[prev] = nil
			}
			selected[i] = &planned{branch: cand, plan: plan}
			break
		}
	}
	// When user interests or shared candidates dominate the top branch across beams, an archetype
	// falls back to its next branch only if it introduces a visit of its own archetype not yet chosen.
	for i := range beams {
		if selected[i] != nil {
			continue
		}
		mask := beams[i].archetype.Mask()
		for ; beams[i].next < len(beams[i].branches); beams[i].next++ {
			cand := beams[i].branches[beams[i].next]
			if !contrasts(cand, mask, selected) {
				continue
			}
			plan, ok, err := p.buildValid(req, policy, route{archetype: beams[i].archetype, branch: cand}, degraded, data)
			if err != nil {
				return nil, err
			}
			if ok {
				selected[i] = &planned{branch: cand, plan: plan}
				break
			}
		}
	}
	var out []domain.Plan
	for _, s := range selected {
		if s != nil {
			out = append(out, s.plan)
		}
	}
	if len(out) > 0 {
		return out, nil
	}
	return p.fallbackPlan(req, policy, beams, degraded, data)
}

func (p *Planner) fallbackPlan(req domain.OptimizeRequest, policy pricing.Policy, beams []archetypeBeam, degraded bool, data domain.DataFreshness) ([]domain.Plan, error) {
	for _, b := range beams {
		for _, cand := range b.branches {
			plan, ok, err := p.buildValid(req, policy, route{archetype: b.archetype, branch: cand}, degraded, data)
			if err != nil {
				return nil, err
			}
			if ok {
				return []domain.Plan{plan}, nil
			}
		}
	}
	return nil, nil
}

func (p *Planner) buildValid(req domain.OptimizeRequest, policy pricing.Policy, r route, degraded bool, data domain.DataFreshness) (domain.Plan, bool, error) {
	plan, in, err := p.assemble(req, policy, r, degraded, data)
	if err != nil {
		return domain.Plan{}, false, err
	}
	if violations := validation.Check(plan, in); len(violations) > 0 {
		rejectedPlans.WithLabelValues(violations[0].Code).Inc()
		p.log.Error("planned route failed validation", zap.String("archetype", string(r.archetype)), zap.Any("violations", violations))
		return domain.Plan{}, false, nil
	}
	return plan, true, nil
}

func hasArchetypeMatch(cand *domain.Branch, mask domain.InterestMask) bool {
	return slices.ContainsFunc(cand.Visits, func(v domain.SearchVisit) bool {
		return v.Candidate.InterestMask().Matches(mask) > 0
	})
}

func contrasts(cand *domain.Branch, mask domain.InterestMask, selected []*planned) bool {
	for _, other := range selected {
		if other != nil && sameVisits(other.branch, cand) {
			return false
		}
	}
	return slices.ContainsFunc(cand.Visits, func(v domain.SearchVisit) bool {
		return v.Candidate.InterestMask().Matches(mask) > 0 && !visitedIn(v, selected)
	})
}

func visitedIn(v domain.SearchVisit, selected []*planned) bool {
	for _, s := range selected {
		if s != nil && slices.ContainsFunc(s.branch.Visits, func(other domain.SearchVisit) bool { return sameVisit(v, other) }) {
			return true
		}
	}
	return false
}

func sameVisits(a, b *domain.Branch) bool {
	return slices.EqualFunc(a.Visits, b.Visits, sameVisit)
}

func sameVisit(a, b domain.SearchVisit) bool {
	return a.Candidate.Place.ID == b.Candidate.Place.ID && sessionOf(a.Candidate) == sessionOf(b.Candidate)
}

func sessionOf(c *domain.Candidate) domain.SessionID {
	if c.Session == nil {
		return domain.SessionID{}
	}
	return c.Session.ID
}

// admissible drops candidates no route may contain whatever else it holds; anchors are judged on
// their own and replace catalog candidates at their places in the search.
func admissible(candidates []domain.Candidate, c domain.RouteConstraints) []domain.Candidate {
	return slices.DeleteFunc(slices.Clone(candidates), func(cand domain.Candidate) bool {
		if slices.Contains(c.ExcludedCategories, cand.Category()) {
			return true
		}
		return cand.Session != nil &&
			(cand.Session.Availability == domain.AvailabilityCancelled || cand.Session.Availability == domain.AvailabilitySoldOut)
	})
}

func points(req domain.OptimizeRequest, pool []domain.Candidate, anchors []solver.Anchor) []domain.Coordinate {
	out := []domain.Coordinate{req.Origin}
	if req.Destination != nil {
		out = append(out, *req.Destination)
	}
	for _, c := range pool {
		out = append(out, c.Place.Location)
	}
	for _, a := range anchors {
		out = append(out, a.Candidate.Place.Location)
	}
	slices.SortFunc(out, func(a, b domain.Coordinate) int {
		if c := cmp.Compare(a.Longitude, b.Longitude); c != 0 {
			return c
		}
		return cmp.Compare(a.Latitude, b.Latitude)
	})
	return slices.Compact(out)
}

func obligationsInfeasible(obligations []domain.Obligation) domain.Conflict {
	c := domain.Conflict{
		Code:    "OBLIGATIONS_INFEASIBLE",
		Message: "No route fits all committed sessions into the day; choose another session, unpin one, or change the interval",
	}
	for _, o := range obligations {
		if o.VisitID != nil {
			c.VisitIDs = append(c.VisitIDs, *o.VisitID)
		}
		if o.SessionID != nil {
			c.SessionIDs = append(c.SessionIDs, *o.SessionID)
		}
	}
	return c
}

func degradedWarning() domain.Warning {
	return domain.Warning{
		Code: "ROUTING_DEGRADED", Scope: domain.ScopeRoute,
		Message: "Travel times are straight-line estimates; rivers and closed areas were not checked",
	}
}

func fewerArchetypesWarning() domain.Warning {
	return domain.Warning{
		Code: "FEWER_ARCHETYPES", Scope: domain.ScopeRoute,
		Message: "The candidate pool does not support three distinct archetype routes under the given constraints",
	}
}

func noFeasibleWarning(pool []domain.Candidate) domain.Warning {
	msg := "No candidate fits the time window, travel modes, and budget"
	if len(pool) == 0 {
		msg = "No available candidates match the requested constraints"
	}
	return domain.Warning{
		Code: "NO_FEASIBLE_ROUTE", Scope: domain.ScopeRoute,
		Message: msg,
	}
}
