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

type legSig struct {
	mode           domain.MovementMode
	depart, arrive time.Time
	distance       float64
	verification   domain.VerificationStatus
}

// legSignature leaves out visit IDs for the same reason as signature.
func legSignature(p domain.Plan) []legSig {
	out := make([]legSig, 0, len(p.Legs))
	for _, l := range p.Legs {
		sig := legSig{mode: l.Mode, depart: l.DepartureAt, arrive: l.ArrivalAt, verification: l.Verification}
		if l.DistanceMeters != nil {
			sig.distance = *l.DistanceMeters
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
				} else if fixedSessionMoved(s, session) {
					fail("route %d moves a fixed session", i)
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

// fixedSessionMoved tells whether a visit left the fixed session's times: it starts with the session,
// or later only when the source lets people in late, and always lasts until the session ends.
func fixedSessionMoved(s domain.Step, session *domain.Session) bool {
	w := session.Window
	if w.Kind != domain.WindowFixed {
		return false
	}
	if !s.VisitEndAt.Equal(w.End) || s.VisitStartAt.Before(w.Start) {
		return true
	}
	lateEntry := w.LateEntryAllowed != nil && *w.LateEntryAllowed
	return !lateEntry && !s.VisitStartAt.Equal(w.Start)
}

// checkRecompute holds a proposal to what already happened, to what the user committed to and to
// the times of fixed sessions still ahead.
func checkRecompute(req domain.RecomputeRequest, pool []domain.Candidate, res domain.RecomputeResult) []string {
	if res.Candidate == nil {
		return nil
	}
	var v []string
	byID := make(map[domain.VisitID]domain.Step, len(res.Candidate.Steps))
	for _, s := range res.Candidate.Steps {
		byID[s.VisitID] = s
	}
	done := map[domain.VisitID]bool{}
	for _, h := range req.History {
		if h.Status != domain.ExecutionCompleted {
			continue
		}
		done[h.VisitID] = true
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
	sessions := make(map[domain.SessionID]*domain.Session)
	for _, c := range pool {
		if c.Session != nil {
			sessions[c.Session.ID] = c.Session
		}
	}
	for _, s := range res.Candidate.Steps {
		if done[s.VisitID] || s.Catalog == nil || s.Catalog.SessionID == nil {
			continue
		}
		if session := sessions[*s.Catalog.SessionID]; session != nil && fixedSessionMoved(s, session) {
			v = append(v, "a fixed session moved")
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

// sessionVisit finds a route with a visit to a catalog session, so a test can spoil that visit.
func sessionVisit(t *testing.T, res domain.OptimizeResult) (route, step int) {
	t.Helper()
	for i, r := range res.Routes {
		if j := slices.IndexFunc(r.Steps, func(s domain.Step) bool { return s.Catalog != nil && s.Catalog.SessionID != nil }); j >= 0 {
			return i, j
		}
	}
	t.Fatal("no route visits a session")
	return 0, 0
}

// setAvailability changes the catalog's copy of the session the visit attends.
func setAvailability(pool []domain.Candidate, id domain.SessionID, availability domain.Availability) {
	for i := range pool {
		if pool[i].Session != nil && pool[i].Session.ID == id {
			session := *pool[i].Session
			session.Availability = availability
			pool[i].Session = &session
		}
	}
}

func TestCheckOptimizeCatchesTampering(t *testing.T) {
	price := int64(100)
	type result = domain.OptimizeResult
	tamper := map[string]func(t *testing.T, req *domain.OptimizeRequest, pool *[]domain.Candidate, res *result){
		"repeated archetype": func(_ *testing.T, _ *domain.OptimizeRequest, _ *[]domain.Candidate, res *result) {
			res.Routes = append(res.Routes, res.Routes[0])
		},
		"same visits under another archetype": func(_ *testing.T, _ *domain.OptimizeRequest, _ *[]domain.Candidate, res *result) {
			copied := res.Routes[0]
			copied.Archetype = domain.ArchetypeActionSocial
			if res.Routes[0].Archetype == domain.ArchetypeActionSocial {
				copied.Archetype = domain.ArchetypeUrbanAvantgarde
			}
			res.Routes = []domain.Plan{res.Routes[0], copied}
		},
		"excluded category": func(t *testing.T, req *domain.OptimizeRequest, _ *[]domain.Candidate, res *result) {
			step := res.Routes[0].Steps[firstVisit(t, res.Routes[0])]
			req.Constraints.ExcludedCategories = []domain.Category{step.Catalog.Category}
		},
		"strict budget": func(t *testing.T, req *domain.OptimizeRequest, _ *[]domain.Candidate, res *result) {
			req.Constraints.Budget = domain.Budget{Mode: domain.BudgetStrict, Limit: &domain.Money{Currency: "RUB"}}
			res.Routes[0].Steps[firstVisit(t, res.Routes[0])].Cost = &domain.CostSnapshot{
				Price: domain.Price{Status: domain.PriceFixed, Currency: "RUB", LowerMinor: &price, UpperMinor: &price},
			}
		},
		"unknown price in ready": func(t *testing.T, req *domain.OptimizeRequest, _ *[]domain.Candidate, res *result) {
			req.Constraints.PushkinCardOnly = true
			res.Routes[0].Result = domain.ResultReady
			res.Routes[0].Steps[firstVisit(t, res.Routes[0])].Cost = &domain.CostSnapshot{Price: domain.Price{Status: domain.PriceUnknown, Currency: "RUB"}}
		},
		"unknown price as zero": func(t *testing.T, _ *domain.OptimizeRequest, _ *[]domain.Candidate, res *result) {
			res.Routes[0].Steps[firstVisit(t, res.Routes[0])].Cost = &domain.CostSnapshot{
				Price:          domain.Price{Status: domain.PriceUnknown, Currency: "RUB"},
				PersonalAmount: &domain.Money{Currency: "RUB"},
			}
		},
		"missing obligation": func(_ *testing.T, req *domain.OptimizeRequest, _ *[]domain.Candidate, _ *result) {
			req.Constraints.Obligations = []domain.Obligation{{SessionID: &domain.SessionID{0xEE}, Participation: domain.ParticipationUserReported}}
		},
		"cancelled session": func(t *testing.T, _ *domain.OptimizeRequest, pool *[]domain.Candidate, res *result) {
			i, j := sessionVisit(t, *res)
			setAvailability(*pool, *res.Routes[i].Steps[j].Catalog.SessionID, domain.AvailabilityCancelled)
		},
		"sold-out session without a held place": func(t *testing.T, _ *domain.OptimizeRequest, pool *[]domain.Candidate, res *result) {
			i, j := sessionVisit(t, *res)
			res.Routes[i].Steps[j].Obligation = false
			setAvailability(*pool, *res.Routes[i].Steps[j].Catalog.SessionID, domain.AvailabilitySoldOut)
		},
		"session outside the catalog": func(t *testing.T, _ *domain.OptimizeRequest, pool *[]domain.Candidate, res *result) {
			i, j := sessionVisit(t, *res)
			id := *res.Routes[i].Steps[j].Catalog.SessionID
			*pool = slices.DeleteFunc(*pool, func(c domain.Candidate) bool { return c.Session != nil && c.Session.ID == id })
		},
		"fixed session moved": func(t *testing.T, _ *domain.OptimizeRequest, _ *[]domain.Candidate, res *result) {
			i, j := sessionVisit(t, *res)
			res.Routes[i].Steps[j].VisitStartAt = res.Routes[i].Steps[j].VisitStartAt.Add(5 * time.Minute)
		},
	}
	for name, change := range tamper {
		t.Run(name, func(t *testing.T) {
			req, pool, res := readyResult(t)
			change(t, &req, &pool, &res)
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
	if v := checkRecompute(req, dayCatalog(), res); len(v) == 0 {
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
	if v := checkRecompute(req, dayCatalog(), domain.RecomputeResult{Status: domain.RecomputeProposed, Candidate: &moved}); len(v) == 0 {
		t.Fatal("moved obligation passed the oracle")
	}
}

func TestCheckRecomputeCatchesShiftedSession(t *testing.T) {
	base := basePlan(t)
	i := slices.IndexFunc(base.Steps, func(s domain.Step) bool { return s.Catalog != nil && s.Catalog.SessionID != nil })
	if i < 0 {
		t.Fatal("base plan has no session")
	}
	// Not a commitment, so only the fixed-session rule can catch the shift.
	base.Steps = slices.Clone(base.Steps)
	base.Steps[i].Obligation = false
	req := reworkRequest(base, domain.PinTrigger{VisitID: base.Steps[0].VisitID, Kind: domain.PinPreferred})
	moved := base
	moved.Steps = slices.Clone(base.Steps)
	moved.Steps[i].VisitStartAt = moved.Steps[i].VisitStartAt.Add(10 * time.Minute)
	res := domain.RecomputeResult{Status: domain.RecomputeProposed, Candidate: &moved}
	if v := checkRecompute(req, dayCatalog(), res); len(v) == 0 {
		t.Fatal("shifted session passed the oracle")
	}
	// A visit already made keeps the times it really had.
	done := moved.Steps[i]
	req.History = []domain.VisitExecution{{VisitID: done.VisitID, Status: domain.ExecutionCompleted, ActualStart: &done.VisitStartAt, ActualEnd: &done.VisitEndAt}}
	if v := checkRecompute(req, dayCatalog(), res); len(v) > 0 {
		t.Fatalf("history judged by the session window: %s", describe(v))
	}
}
