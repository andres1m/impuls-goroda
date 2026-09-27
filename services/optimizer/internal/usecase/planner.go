package usecase

import (
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

type Config struct {
	Currency    string `yaml:"currency"`
	BeamWidth   int    `yaml:"beam-width"`
	Parallelism int    `yaml:"parallelism"`
}

// Planner builds routes: one search per archetype, each result assembled into a plan and
// released only after the independent validation accepts it.
type Planner struct {
	cfg     Config
	source  CandidateSource
	transit TransitProvider
	log     *zap.Logger
	newID   func() domain.VisitID
}

func NewPlanner(cfg Config, source CandidateSource, transit TransitProvider, log *zap.Logger) (*Planner, error) {
	if err := (solver.Config{BeamWidth: cfg.BeamWidth, Parallelism: cfg.Parallelism}).Validate(); err != nil {
		return nil, err
	}
	if err := (domain.Money{Currency: cfg.Currency}).Validate(); err != nil {
		return nil, err
	}
	if source == nil || transit == nil || log == nil {
		return nil, errors.New("planner needs a candidate source, a transit provider and a logger")
	}
	return &Planner{cfg: cfg, source: source, transit: transit, log: log, newID: randomVisitID}, nil
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
	pool := admissible(candidates, req.Constraints)
	transit, degraded, err := p.transit.Transit(ctx, req.City, points(req, pool, anchors), req.Constraints.MovementModes)
	if err != nil {
		return domain.OptimizeResult{}, err
	}
	s, err := solver.New(solver.Config{BeamWidth: p.cfg.BeamWidth, Parallelism: p.cfg.Parallelism}, solver.DefaultScoreParams(), transit, solver.WindowPlacement{})
	if err != nil {
		return domain.OptimizeResult{}, err
	}
	problem := solver.Problem{
		Start: req.Start, End: req.End, Origin: req.Origin, Destination: req.Destination,
		Interests: req.Constraints.InterestMask, Modes: req.Constraints.MovementModes,
		Pricing: policy, Anchors: anchors, Archetype: archetypes[0],
	}
	if conflicts, err := s.Diagnose(ctx, problem); err != nil {
		return domain.OptimizeResult{}, err
	} else if len(conflicts) > 0 {
		return done(domain.OptimizeResult{Status: domain.ResultConflict, Conflicts: conflicts})
	}

	routes, err := p.routes(ctx, s, problem, pool)
	if err != nil {
		return domain.OptimizeResult{}, err
	}
	var plans []domain.Plan
	for _, route := range routes {
		plan, in, err := p.assemble(req, policy, route, degraded, data)
		if err != nil {
			return domain.OptimizeResult{}, err
		}
		if violations := validation.Check(plan, in); len(violations) > 0 {
			rejectedPlans.WithLabelValues(violations[0].Code).Inc()
			p.log.Error("planned route failed validation", zap.String("archetype", string(route.archetype)), zap.Any("violations", violations))
			continue
		}
		plans = append(plans, plan)
	}

	var warnings []domain.Warning
	if degraded {
		warnings = append(warnings, degradedWarning())
	}
	if len(plans) == 0 {
		if len(anchors) > 0 {
			obligations := make([]domain.Obligation, len(anchors))
			for i, a := range anchors {
				obligations[i] = a.Obligation
			}
			return done(domain.OptimizeResult{Status: domain.ResultConflict, Warnings: warnings, Conflicts: []domain.Conflict{obligationsInfeasible(obligations)}})
		}
		return done(domain.OptimizeResult{Status: domain.ResultNoFeasibleRoute, Warnings: warnings})
	}
	status := domain.ResultPartial
	if slices.ContainsFunc(plans, func(plan domain.Plan) bool { return plan.Result == domain.ResultReady }) {
		status = domain.ResultReady
	}
	return done(domain.OptimizeResult{Status: status, Routes: plans, Warnings: warnings})
}

func (*Planner) Recompute(context.Context, domain.RecomputeRequest) (domain.RecomputeResult, error) {
	return domain.RecomputeResult{}, ErrNotImplemented
}

type route struct {
	archetype domain.Archetype
	branch    *domain.Branch
}

// routes runs one search per archetype and keeps each distinct sequence of visits once, under the
// archetype that scored it highest.
func (p *Planner) routes(ctx context.Context, s *solver.Solver, problem solver.Problem, pool []domain.Candidate) ([]route, error) {
	var found []route
	for _, a := range archetypes {
		problem.Archetype = a
		branches, err := s.Search(ctx, problem, pool)
		if err != nil {
			return nil, err
		}
		if len(branches) == 0 {
			continue
		}
		best := branches[0]
		i := slices.IndexFunc(found, func(r route) bool { return sameVisits(r.branch, best) })
		switch {
		case i < 0:
			found = append(found, route{archetype: a, branch: best})
		case best.Score > found[i].branch.Score:
			found[i] = route{archetype: a, branch: best}
		}
	}
	return found, nil
}

func sameVisits(a, b *domain.Branch) bool {
	return slices.EqualFunc(a.Visits, b.Visits, func(x, y domain.SearchVisit) bool {
		return x.Candidate.Place.ID == y.Candidate.Place.ID && sessionOf(x.Candidate) == sessionOf(y.Candidate)
	})
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
		if a.Longitude != b.Longitude {
			return compare(a.Longitude, b.Longitude)
		}
		return compare(a.Latitude, b.Latitude)
	})
	return slices.Compact(out)
}

func compare(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
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
