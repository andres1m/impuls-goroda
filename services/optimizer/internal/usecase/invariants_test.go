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

func plannerWithLog(t testing.TB, c *genCase, parallelism int) (*Planner, *observer.ObservedLogs) {
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
func signature(p *domain.Plan) []visitSig {
	out := make([]visitSig, 0, len(p.Steps))
	for i := range p.Steps {
		s := &p.Steps[i]
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
func legSignature(p *domain.Plan) []legSig {
	out := make([]legSig, 0, len(p.Legs))
	for i := range p.Legs {
		l := &p.Legs[i]
		sig := legSig{mode: l.Mode, depart: l.DepartureAt, arrive: l.ArrivalAt, verification: l.Verification}
		if l.DistanceMeters != nil {
			sig.distance = *l.DistanceMeters
		}
		out = append(out, sig)
	}
	return out
}

// checkOptimize states the hard rules through the request, independently of the validator's input.
//
//nolint:gocritic // test helper inspects an owned result value
func checkOptimize(req *domain.OptimizeRequest, pool []domain.Candidate, res domain.OptimizeResult) []string {
	var v []string
	fail := func(format string, args ...any) { v = append(v, fmt.Sprintf(format, args...)) }
	sessions := make(map[domain.SessionID]*domain.Session)
	for i := range pool {
		if pool[i].Session != nil {
			sessions[pool[i].Session.ID] = pool[i].Session
		}
	}
	seen := map[domain.Archetype]bool{}
	for i := range res.Routes {
		route := &res.Routes[i]
		if seen[route.Archetype] {
			fail("route %d repeats archetype %s", i, route.Archetype)
		}
		seen[route.Archetype] = true
		for j := range i {
			if slices.Equal(signature(route), signature(&res.Routes[j])) {
				fail("routes %d and %d make the same visits", j, i)
			}
		}
		checkRouteSteps(req, route, sessions, i, fail)
		for _, o := range req.Constraints.Obligations {
			if o.SessionID == nil {
				continue
			}
			if !slices.ContainsFunc(route.Steps, func(s domain.Step) bool {
				return s.Obligation && s.Catalog != nil && s.Catalog.SessionID != nil &&
					*s.Catalog.SessionID == *o.SessionID
			}) {
				fail("route %d drops an obligation", i)
			}
		}
	}
	conflicts := slices.Clone(res.Conflicts)
	for i := range res.Routes {
		conflicts = append(conflicts, res.Routes[i].Conflicts...)
	}
	checkSoldOutConflicts(req, conflicts, sessions, fail)
	return v
}

// checkRouteSteps checks budget and session rules for every step of one route.
func checkRouteSteps(
	req *domain.OptimizeRequest,
	route *domain.Plan,
	sessions map[domain.SessionID]*domain.Session,
	i int,
	fail func(string, ...any),
) {
	var upper int64
	for j := range route.Steps {
		s := &route.Steps[j]
		if s.Catalog == nil {
			continue
		}
		if !s.Obligation && slices.Contains(req.Constraints.ExcludedCategories, s.Catalog.Category) {
			fail("route %d visits excluded %s", i, s.Catalog.Category)
		}
		if s.Catalog.SessionID != nil {
			checkSessionVisit(sessions, s, i, fail)
		}
		if s.Cost == nil {
			continue
		}
		meal := slices.ContainsFunc(s.AppliedConstraints, func(c domain.AppliedConstraint) bool {
			return c.Code == lunchWindowCode
		})
		switch {
		case s.Cost.Price.Status == domain.PriceUnknown:
			if !meal && route.Result == domain.ResultReady &&
				(req.Constraints.Budget.Mode == domain.BudgetStrict || req.Constraints.PushkinCardOnly) {
				fail("route %d is ready on an unknown price under a strict budget or card-only mode", i)
			}
			if s.Cost.PersonalAmount != nil && s.Cost.PersonalAmount.AmountMinor == 0 {
				fail("route %d turns an unknown price into zero", i)
			}
		case !meal && s.Cost.Price.UpperMinor != nil:
			upper += *s.Cost.Price.UpperMinor
		}
	}
	if b := req.Constraints.Budget; b.Mode == domain.BudgetStrict && upper > b.Limit.AmountMinor {
		fail("route %d spends %d over the strict limit %d", i, upper, b.Limit.AmountMinor)
	}
}

// checkSessionVisit checks availability and timing rules for a step that attends a session.
func checkSessionVisit(
	sessions map[domain.SessionID]*domain.Session,
	s *domain.Step,
	i int,
	fail func(string, ...any),
) {
	session := sessions[*s.Catalog.SessionID]
	switch {
	case session == nil:
		fail("route %d visits a session outside the catalog", i)
	case session.Availability == domain.AvailabilityCancelled:
		fail("route %d visits a cancelled session", i)
	case session.Availability == domain.AvailabilitySoldOut && !s.Obligation:
		fail("route %d visits a sold-out session without a held place", i)
	case fixedSessionMoved(s, session):
		fail("route %d moves a fixed session", i)
	}
}

// checkSoldOutConflicts validates that sold-out conflict codes refer to genuinely sold-out sessions.
func checkSoldOutConflicts(
	req *domain.OptimizeRequest,
	conflicts []domain.Conflict,
	sessions map[domain.SessionID]*domain.Session,
	fail func(string, ...any),
) {
	for _, c := range conflicts {
		if c.Code != "OBLIGATION_SOLD_OUT" {
			continue
		}
		for _, id := range c.SessionIDs {
			if session := sessions[id]; session == nil || session.Availability != domain.AvailabilitySoldOut {
				fail("an obligation is refused as sold out, but its session is not")
			}
			if slices.ContainsFunc(req.Constraints.Obligations, func(o domain.Obligation) bool {
				return o.SessionID != nil && *o.SessionID == id && holdsPlace(o.Participation)
			}) {
				fail("an obligation with a held place is refused as sold out")
			}
		}
	}
}

// holdsPlace tells whether the user already has a place, which a sold-out session does not take away.
func holdsPlace(status domain.ParticipationStatus) bool {
	return status == domain.ParticipationUserReported || status == domain.ParticipationProviderConfirmed
}

// lateEntrySession tells whether the visit attends a session the source lets people into late, within its
// rules: a repaired day may enter such a commitment at another moment than before, later after a
// delay or at the start once the day frees time.
func lateEntrySession(s *domain.Step, sessions map[domain.SessionID]*domain.Session) bool {
	if s.Catalog == nil || s.Catalog.SessionID == nil {
		return false
	}
	session := sessions[*s.Catalog.SessionID]
	return session != nil && session.Window.LateEntryAllowed != nil && *session.Window.LateEntryAllowed &&
		!fixedSessionMoved(s, session)
}

// fixedSessionMoved tells whether a visit left the fixed session's times: it starts with the session,
// or later only when the source lets people in late and not after the last entry, and always lasts
// until the session ends.
func fixedSessionMoved(s *domain.Step, session *domain.Session) bool {
	w := session.Window
	if w.Kind != domain.WindowFixed {
		return false
	}
	if !s.VisitEndAt.Equal(w.End) || s.VisitStartAt.Before(w.Start) {
		return true
	}
	if s.VisitStartAt.Equal(w.Start) {
		return false
	}
	if w.LateEntryAllowed == nil || !*w.LateEntryAllowed {
		return true
	}
	lastEntry := w.End.Add(-w.MinDuration)
	if w.LastEntryAt != nil {
		lastEntry = *w.LastEntryAt
	}
	return s.VisitStartAt.After(lastEntry)
}

// checkRecompute holds a proposal to what already happened, to what the user committed to and to
// the times of fixed sessions still ahead.
//
//nolint:gocritic // test helper inspects an owned result value
func checkRecompute(req *domain.RecomputeRequest, pool []domain.Candidate, res domain.RecomputeResult) []string {
	if res.Candidate == nil {
		return nil
	}
	var v []string
	byID := make(map[domain.VisitID]domain.Step, len(res.Candidate.Steps))
	for i := range res.Candidate.Steps {
		s := &res.Candidate.Steps[i]
		byID[s.VisitID] = *s
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
	sessions := make(map[domain.SessionID]*domain.Session)
	for i := range pool {
		if pool[i].Session != nil {
			sessions[pool[i].Session.ID] = pool[i].Session
		}
	}
	checkRecomputeObligations(req, byID, sessions, done, &v)
	return v
}

// checkRecomputeObligations checks that obligations and fixed sessions are respected.
func checkRecomputeObligations(
	req *domain.RecomputeRequest,
	byID map[domain.VisitID]domain.Step,
	sessions map[domain.SessionID]*domain.Session,
	done map[domain.VisitID]bool,
	v *[]string,
) {
	for i := range req.Base.Steps {
		b := &req.Base.Steps[i]
		if !b.Obligation {
			continue
		}
		s, ok := byID[b.VisitID]
		sp := &s
		if !ok || (s.VisitStartAt.Equal(b.VisitStartAt) && s.VisitEndAt.Equal(b.VisitEndAt)) ||
			lateEntrySession(sp, sessions) {
			continue
		}
		*v = append(*v, "an obligation moved")
	}
	// NOTE: Candidate steps are accessed via byID (value copy); use a local copy to get a pointer.
	for id := range byID {
		sp := byID[id]
		if done[id] || sp.Catalog == nil || sp.Catalog.SessionID == nil {
			continue
		}
		if session := sessions[*sp.Catalog.SessionID]; session != nil && fixedSessionMoved(&sp, session) {
			*v = append(*v, "a fixed session moved")
		}
	}
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

func firstVisit(t *testing.T, p *domain.Plan) int {
	t.Helper()
	i := slices.IndexFunc(p.Steps, func(s domain.Step) bool { return s.Catalog != nil })
	if i < 0 {
		t.Fatal("route has no visits")
	}
	return i
}

func TestCheckOptimizeAcceptsRealResult(t *testing.T) {
	req, pool, res := readyResult(t)
	if v := checkOptimize(&req, pool, res); len(v) > 0 {
		t.Fatal(describe(v))
	}
}

// sessionVisit finds a route with a visit to a catalog session, so a test can spoil that visit.
func sessionVisit(t *testing.T, res *domain.OptimizeResult) (route, step int) {
	t.Helper()
	for i := range res.Routes {
		if j := slices.IndexFunc(
			res.Routes[i].Steps,
			func(s domain.Step) bool { return s.Catalog != nil && s.Catalog.SessionID != nil },
		); j >= 0 {
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

// setWindow changes the window of the catalog's copy of the session.
func setWindow(pool []domain.Candidate, id domain.SessionID, change func(w *domain.VisitWindow)) {
	for i := range pool {
		if pool[i].Session != nil && pool[i].Session.ID == id {
			session := *pool[i].Session
			change(&session.Window)
			pool[i].Session = &session
			pool[i].Window = session.Window
		}
	}
}

// soldOutRefusal turns the result into a refusal of an obligation to a sold-out session, held as given.
func soldOutRefusal(
	t *testing.T,
	req *domain.OptimizeRequest,
	pool []domain.Candidate,
	res *domain.OptimizeResult,
	held domain.ParticipationStatus,
) {
	t.Helper()
	i := slices.IndexFunc(pool, func(c domain.Candidate) bool { return c.Session != nil })
	if i < 0 {
		t.Fatal("no sessions in the catalog")
	}
	id := pool[i].Session.ID
	setAvailability(pool, id, domain.AvailabilitySoldOut)
	req.Constraints.Obligations = []domain.Obligation{{SessionID: &id, Participation: held}}
	res.Routes = nil
	res.Status = domain.ResultConflict
	res.Conflicts = []domain.Conflict{
		{Code: "OBLIGATION_SOLD_OUT", SessionIDs: []domain.SessionID{id}, Message: "No places are left"},
	}
}

func TestCheckOptimizeAcceptsSoldOutRefusal(t *testing.T) {
	req, pool, res := readyResult(t)
	soldOutRefusal(t, &req, pool, &res, domain.ParticipationActionRequired)
	if v := checkOptimize(&req, pool, res); len(v) > 0 {
		t.Fatal(describe(v))
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
			step := res.Routes[0].Steps[firstVisit(t, &res.Routes[0])]
			req.Constraints.ExcludedCategories = []domain.Category{step.Catalog.Category}
		},
		"strict budget": func(t *testing.T, req *domain.OptimizeRequest, _ *[]domain.Candidate, res *result) {
			req.Constraints.Budget = domain.Budget{Mode: domain.BudgetStrict, Limit: &domain.Money{Currency: "RUB"}}
			res.Routes[0].Steps[firstVisit(t, &res.Routes[0])].Cost = &domain.CostSnapshot{
				Price: domain.Price{Status: domain.PriceFixed, Currency: "RUB", LowerMinor: &price, UpperMinor: &price},
			}
		},
		"unknown price in ready": func(t *testing.T, req *domain.OptimizeRequest, _ *[]domain.Candidate, res *result) {
			req.Constraints.PushkinCardOnly = true
			res.Routes[0].Result = domain.ResultReady
			res.Routes[0].Steps[firstVisit(t, &res.Routes[0])].Cost = &domain.CostSnapshot{
				Price: domain.Price{Status: domain.PriceUnknown, Currency: "RUB"},
			}
		},
		"unknown price as zero": func(t *testing.T, _ *domain.OptimizeRequest, _ *[]domain.Candidate, res *result) {
			res.Routes[0].Steps[firstVisit(t, &res.Routes[0])].Cost = &domain.CostSnapshot{
				Price:          domain.Price{Status: domain.PriceUnknown, Currency: "RUB"},
				PersonalAmount: &domain.Money{Currency: "RUB"},
			}
		},
		"missing obligation": func(_ *testing.T, req *domain.OptimizeRequest, _ *[]domain.Candidate, _ *result) {
			req.Constraints.Obligations = []domain.Obligation{
				{SessionID: &domain.SessionID{0xEE}, Participation: domain.ParticipationUserReported},
			}
		},
		"cancelled session": func(t *testing.T, _ *domain.OptimizeRequest, pool *[]domain.Candidate, res *result) {
			i, j := sessionVisit(t, res)
			setAvailability(*pool, *res.Routes[i].Steps[j].Catalog.SessionID, domain.AvailabilityCancelled)
		},
		"sold-out session without a held place": func(t *testing.T, _ *domain.OptimizeRequest, pool *[]domain.Candidate, res *result) {
			i, j := sessionVisit(t, res)
			res.Routes[i].Steps[j].Obligation = false
			setAvailability(*pool, *res.Routes[i].Steps[j].Catalog.SessionID, domain.AvailabilitySoldOut)
		},
		"session outside the catalog": func(t *testing.T, _ *domain.OptimizeRequest, pool *[]domain.Candidate, res *result) {
			i, j := sessionVisit(t, res)
			id := *res.Routes[i].Steps[j].Catalog.SessionID
			*pool = slices.DeleteFunc(
				*pool,
				func(c domain.Candidate) bool { return c.Session != nil && c.Session.ID == id },
			)
		},
		"fixed session moved": func(t *testing.T, _ *domain.OptimizeRequest, _ *[]domain.Candidate, res *result) {
			i, j := sessionVisit(t, res)
			res.Routes[i].Steps[j].VisitStartAt = res.Routes[i].Steps[j].VisitStartAt.Add(5 * time.Minute)
		},
		"late entry after the last entry": func(t *testing.T, _ *domain.OptimizeRequest, pool *[]domain.Candidate, res *result) {
			i, j := sessionVisit(t, res)
			step := &res.Routes[i].Steps[j]
			late, lastEntry := true, step.VisitStartAt.Add(time.Minute)
			setWindow(*pool, *step.Catalog.SessionID, func(w *domain.VisitWindow) {
				w.LateEntryAllowed, w.LastEntryAt, w.MinDuration = &late, &lastEntry, time.Minute
			})
			step.VisitStartAt = step.VisitStartAt.Add(5 * time.Minute)
		},
		"sold-out refusal of a held place": func(t *testing.T, req *domain.OptimizeRequest, pool *[]domain.Candidate, res *result) {
			soldOutRefusal(t, req, *pool, res, domain.ParticipationUserReported)
		},
		"sold-out refusal of an available session": func(t *testing.T, req *domain.OptimizeRequest, pool *[]domain.Candidate, res *result) {
			soldOutRefusal(t, req, *pool, res, domain.ParticipationActionRequired)
			setAvailability(*pool, res.Conflicts[0].SessionIDs[0], domain.AvailabilityAvailable)
		},
	}
	for name, change := range tamper {
		t.Run(name, func(t *testing.T) {
			req, pool, res := readyResult(t)
			change(t, &req, &pool, &res)
			if v := checkOptimize(&req, pool, res); len(v) == 0 {
				t.Fatal("tampered result passed the oracle")
			}
		})
	}
}

func TestCheckRecomputeCatchesMovedHistory(t *testing.T) {
	base := basePlan(t)
	done := base.Steps[0]
	req := reworkRequest(&base, domain.PinTrigger{VisitID: base.Steps[1].VisitID, Kind: domain.PinPreferred})
	req.History = []domain.VisitExecution{
		{
			VisitID:     done.VisitID,
			Status:      domain.ExecutionCompleted,
			ActualStart: &done.VisitStartAt,
			ActualEnd:   &done.VisitEndAt,
		},
	}
	moved := base
	moved.Steps = slices.Clone(base.Steps)
	moved.Steps[0].VisitStartAt = done.VisitStartAt.Add(time.Minute)
	res := domain.RecomputeResult{Status: domain.RecomputeProposed, Candidate: &moved}
	if v := checkRecompute(&req, dayCatalog(), res); len(v) == 0 {
		t.Fatal("moved history passed the oracle")
	}
}

func TestCheckRecomputeCatchesMovedObligation(t *testing.T) {
	base := basePlan(t)
	i := slices.IndexFunc(base.Steps, func(s domain.Step) bool { return s.Obligation })
	if i < 0 {
		t.Fatal("base plan has no obligation")
	}
	req := reworkRequest(&base, domain.PinTrigger{VisitID: base.Steps[0].VisitID, Kind: domain.PinPreferred})
	moved := base
	moved.Steps = slices.Clone(base.Steps)
	moved.Steps[i].VisitStartAt = moved.Steps[i].VisitStartAt.Add(10 * time.Minute)
	if v := checkRecompute(
		&req,
		dayCatalog(),
		domain.RecomputeResult{Status: domain.RecomputeProposed, Candidate: &moved},
	); len(
		v,
	) == 0 {
		t.Fatal("moved obligation passed the oracle")
	}
}

func TestCheckRecomputeAcceptsLateEntryToAnObligation(t *testing.T) {
	base := basePlan(t)
	i := slices.IndexFunc(
		base.Steps,
		func(s domain.Step) bool { return s.Obligation && s.Catalog != nil && s.Catalog.SessionID != nil },
	)
	if i < 0 {
		t.Fatal("base plan has no obligation to a session")
	}
	req := reworkRequest(&base, domain.PinTrigger{VisitID: base.Steps[0].VisitID, Kind: domain.PinPreferred})
	pool := dayCatalog()
	late := true
	setWindow(pool, *base.Steps[i].Catalog.SessionID, func(w *domain.VisitWindow) {
		w.LateEntryAllowed, w.MinDuration = &late, w.End.Sub(w.Start)/2
	})
	moved := base
	moved.Steps = slices.Clone(base.Steps)
	moved.Steps[i].VisitStartAt = moved.Steps[i].VisitStartAt.Add(10 * time.Minute)
	if v := checkRecompute(
		&req,
		pool,
		domain.RecomputeResult{Status: domain.RecomputeProposed, Candidate: &moved},
	); len(
		v,
	) > 0 {
		t.Fatalf("late entry the source allows: %s", describe(v))
	}
}

func TestCheckRecomputeCatchesShiftedSession(t *testing.T) {
	base := basePlan(t)
	i := slices.IndexFunc(
		base.Steps,
		func(s domain.Step) bool { return s.Catalog != nil && s.Catalog.SessionID != nil },
	)
	if i < 0 {
		t.Fatal("base plan has no session")
	}
	// Not a commitment, so only the fixed-session rule can catch the shift.
	base.Steps = slices.Clone(base.Steps)
	base.Steps[i].Obligation = false
	req := reworkRequest(&base, domain.PinTrigger{VisitID: base.Steps[0].VisitID, Kind: domain.PinPreferred})
	moved := base
	moved.Steps = slices.Clone(base.Steps)
	moved.Steps[i].VisitStartAt = moved.Steps[i].VisitStartAt.Add(10 * time.Minute)
	res := domain.RecomputeResult{Status: domain.RecomputeProposed, Candidate: &moved}
	if v := checkRecompute(&req, dayCatalog(), res); len(v) == 0 {
		t.Fatal("shifted session passed the oracle")
	}
	// A visit already made keeps the times it really had.
	done := moved.Steps[i]
	req.History = []domain.VisitExecution{
		{
			VisitID:     done.VisitID,
			Status:      domain.ExecutionCompleted,
			ActualStart: &done.VisitStartAt,
			ActualEnd:   &done.VisitEndAt,
		},
	}
	if v := checkRecompute(&req, dayCatalog(), res); len(v) > 0 {
		t.Fatalf("history judged by the session window: %s", describe(v))
	}
}
