package usecase

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func plannerWithLog(t testing.TB, c genCase, parallelism int) (*Planner, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zapcore.ErrorLevel)
	cfg := config()
	cfg.BeamWidth, cfg.Parallelism = 16, parallelism
	p, err := NewPlanner(cfg, fakeSource{candidates: c.pool}, c.provider, zap.New(core))
	if err != nil {
		t.Fatal(err)
	}
	return p, logs
}

// rejections counts routes the built-in validator had to throw away: the search and assembly
// must never lean on it, so any count above zero is a broken hard rule.
func rejections(logs *observer.ObservedLogs) int {
	return logs.FilterMessageSnippet("failed validation").Len()
}

type visitSig struct {
	place      domain.PlaceID
	session    domain.SessionID
	start, end time.Time
}

// signature leaves out visit IDs: the planner draws them at random on every call.
func signature(p domain.Plan) []visitSig {
	out := make([]visitSig, 0, len(p.Steps))
	for _, s := range p.Steps {
		sig := visitSig{start: s.VisitStartAt, end: s.VisitEndAt}
		if s.Catalog != nil {
			sig.place = s.Catalog.PlaceID
			if s.Catalog.SessionID != nil {
				sig.session = *s.Catalog.SessionID
			}
		}
		out = append(out, sig)
	}
	return out
}

// checkOptimize states the hard rules through the request, independently of the validator's input.
func checkOptimize(req domain.OptimizeRequest, pool []domain.Candidate, res domain.OptimizeResult) []string {
	var v []string
	fail := func(format string, args ...any) { v = append(v, fmt.Sprintf(format, args...)) }
	sessions := make(map[domain.SessionID]*domain.Session)
	for _, c := range pool {
		if c.Session != nil {
			sessions[c.Session.ID] = c.Session
		}
	}
	seen := map[domain.Archetype]bool{}
	for i, route := range res.Routes {
		if seen[route.Archetype] {
			fail("route %d repeats archetype %s", i, route.Archetype)
		}
		seen[route.Archetype] = true
		for j := range i {
			if slices.Equal(signature(route), signature(res.Routes[j])) {
				fail("routes %d and %d make the same visits", j, i)
			}
		}
		var upper int64
		for _, s := range route.Steps {
			if s.Catalog == nil {
				continue
			}
			if !s.Obligation && slices.Contains(req.Constraints.ExcludedCategories, s.Catalog.Category) {
				fail("route %d visits excluded %s", i, s.Catalog.Category)
			}
			if s.Catalog.SessionID != nil {
				if session := sessions[*s.Catalog.SessionID]; session == nil {
					fail("route %d visits a session outside the catalog", i)
				} else if session.Availability == domain.AvailabilityCancelled {
					fail("route %d visits a cancelled session", i)
				} else if session.Availability == domain.AvailabilitySoldOut && !s.Obligation {
					fail("route %d visits a sold-out session without a held place", i)
				}
			}
			if s.Cost == nil {
				continue
			}
			if s.Cost.Price.Status == domain.PriceUnknown {
				if route.Result == domain.ResultReady && (req.Constraints.Budget.Mode == domain.BudgetStrict || req.Constraints.PushkinCardOnly) {
					fail("route %d is ready on an unknown price under a strict budget or card-only mode", i)
				}
				if s.Cost.PersonalAmount != nil && s.Cost.PersonalAmount.AmountMinor == 0 {
					fail("route %d turns an unknown price into zero", i)
				}
			} else if s.Cost.Price.UpperMinor != nil {
				upper += *s.Cost.Price.UpperMinor
			}
		}
		if b := req.Constraints.Budget; b.Mode == domain.BudgetStrict && upper > b.Limit.AmountMinor {
			fail("route %d spends %d over the strict limit %d", i, upper, b.Limit.AmountMinor)
		}
		for _, o := range req.Constraints.Obligations {
			if o.SessionID == nil {
				continue
			}
			if !slices.ContainsFunc(route.Steps, func(s domain.Step) bool {
				return s.Obligation && s.Catalog != nil && s.Catalog.SessionID != nil && *s.Catalog.SessionID == *o.SessionID
			}) {
				fail("route %d drops an obligation", i)
			}
		}
	}
	return v
}

// checkRecompute holds a proposal to what already happened and to what the user committed to.
func checkRecompute(req domain.RecomputeRequest, res domain.RecomputeResult) []string {
	if res.Candidate == nil {
		return nil
	}
	var v []string
	byID := make(map[domain.VisitID]domain.Step, len(res.Candidate.Steps))
	for _, s := range res.Candidate.Steps {
		byID[s.VisitID] = s
	}
	for _, h := range req.History {
		if h.Status != domain.ExecutionCompleted {
			continue
		}
		s, ok := byID[h.VisitID]
		if !ok || !s.VisitStartAt.Equal(*h.ActualStart) || !s.VisitEndAt.Equal(*h.ActualEnd) {
			v = append(v, "a completed visit changed or disappeared")
		}
	}
	for _, b := range req.Base.Steps {
		if !b.Obligation {
			continue
		}
		if s, ok := byID[b.VisitID]; ok && (!s.VisitStartAt.Equal(b.VisitStartAt) || !s.VisitEndAt.Equal(b.VisitEndAt)) {
			v = append(v, "an obligation moved")
		}
	}
	return v
}

func describe(v []string) string {
	return strings.Join(v, "; ")
}

func readyResult(t *testing.T) (domain.OptimizeRequest, []domain.Candidate, domain.OptimizeResult) {
	t.Helper()
	res := optimize(t, city(), estimated(), nil)
	if len(res.Routes) == 0 {
		t.Fatalf("no routes: %s", res.Status)
	}
	return request(), city(), res
}

func firstVisit(t *testing.T, p domain.Plan) int {
	t.Helper()
	i := slices.IndexFunc(p.Steps, func(s domain.Step) bool { return s.Catalog != nil })
	if i < 0 {
		t.Fatal("route has no visits")
	}
	return i
}

func TestCheckOptimizeAcceptsRealResult(t *testing.T) {
	req, pool, res := readyResult(t)
	if v := checkOptimize(req, pool, res); len(v) > 0 {
		t.Fatal(describe(v))
	}
}

func TestCheckOptimizeCatchesTampering(t *testing.T) {
	price := int64(100)
	tamper := map[string]func(t *testing.T, req *domain.OptimizeRequest, res *domain.OptimizeResult){
		"repeated archetype": func(_ *testing.T, _ *domain.OptimizeRequest, res *domain.OptimizeResult) {
			res.Routes = append(res.Routes, res.Routes[0])
		},
		"excluded category": func(t *testing.T, req *domain.OptimizeRequest, res *domain.OptimizeResult) {
			step := res.Routes[0].Steps[firstVisit(t, res.Routes[0])]
			req.Constraints.ExcludedCategories = []domain.Category{step.Catalog.Category}
		},
		"strict budget": func(t *testing.T, req *domain.OptimizeRequest, res *domain.OptimizeResult) {
			req.Constraints.Budget = domain.Budget{Mode: domain.BudgetStrict, Limit: &domain.Money{Currency: "RUB"}}
			res.Routes[0].Steps[firstVisit(t, res.Routes[0])].Cost = &domain.CostSnapshot{
				Price: domain.Price{Status: domain.PriceFixed, Currency: "RUB", LowerMinor: &price, UpperMinor: &price},
			}
		},
		"unknown price in ready": func(t *testing.T, req *domain.OptimizeRequest, res *domain.OptimizeResult) {
			req.Constraints.PushkinCardOnly = true
			res.Routes[0].Result = domain.ResultReady
			res.Routes[0].Steps[firstVisit(t, res.Routes[0])].Cost = &domain.CostSnapshot{Price: domain.Price{Status: domain.PriceUnknown, Currency: "RUB"}}
		},
		"missing obligation": func(_ *testing.T, req *domain.OptimizeRequest, _ *domain.OptimizeResult) {
			req.Constraints.Obligations = []domain.Obligation{{SessionID: &domain.SessionID{0xEE}, Participation: domain.ParticipationUserReported}}
		},
	}
	for name, change := range tamper {
		t.Run(name, func(t *testing.T) {
			req, pool, res := readyResult(t)
			change(t, &req, &res)
			if v := checkOptimize(req, pool, res); len(v) == 0 {
				t.Fatal("tampered result passed the oracle")
			}
		})
	}
}

func TestCheckRecomputeCatchesMovedHistory(t *testing.T) {
	base := basePlan(t)
	done := base.Steps[0]
	req := reworkRequest(base, domain.PinTrigger{VisitID: base.Steps[1].VisitID, Kind: domain.PinPreferred})
	req.History = []domain.VisitExecution{{VisitID: done.VisitID, Status: domain.ExecutionCompleted, ActualStart: &done.VisitStartAt, ActualEnd: &done.VisitEndAt}}
	moved := base
	moved.Steps = slices.Clone(base.Steps)
	moved.Steps[0].VisitStartAt = done.VisitStartAt.Add(time.Minute)
	res := domain.RecomputeResult{Status: domain.RecomputeProposed, Candidate: &moved}
	if v := checkRecompute(req, res); len(v) == 0 {
		t.Fatal("moved history passed the oracle")
	}
}

func TestCheckRecomputeCatchesMovedObligation(t *testing.T) {
	base := basePlan(t)
	i := slices.IndexFunc(base.Steps, func(s domain.Step) bool { return s.Obligation })
	if i < 0 {
		t.Fatal("base plan has no obligation")
	}
	req := reworkRequest(base, domain.PinTrigger{VisitID: base.Steps[0].VisitID, Kind: domain.PinPreferred})
	moved := base
	moved.Steps = slices.Clone(base.Steps)
	moved.Steps[i].VisitStartAt = moved.Steps[i].VisitStartAt.Add(10 * time.Minute)
	if v := checkRecompute(req, domain.RecomputeResult{Status: domain.RecomputeProposed, Candidate: &moved}); len(v) == 0 {
		t.Fatal("moved obligation passed the oracle")
	}
}
