package solver

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func obligationFor(c *domain.Candidate) domain.Obligation {
	id := c.Session.ID
	return domain.Obligation{SessionID: &id, Participation: domain.ParticipationUserReported}
}

func anchor(c *domain.Candidate) Anchor {
	return Anchor{Candidate: *c, Obligation: obligationFor(c)}
}

func withAnchors(p *Problem, anchors ...Anchor) *Problem {
	out := *p
	out.Anchors = anchors
	return &out
}

func requireInEveryRoute(t *testing.T, routes []*domain.Branch, ids ...byte) {
	t.Helper()
	if len(routes) == 0 {
		t.Fatal("no routes")
	}
	for _, r := range routes {
		for _, id := range ids {
			if !slices.Contains(placeIDs(r), id) {
				t.Fatalf("route %v misses anchor %d", placeIDs(r), id)
			}
		}
	}
}

func TestSearchKeepsLowScoreAnchor(t *testing.T) {
	concert := session(9, domain.CategoryCulture, north(origin, 3000), at(15, 0), at(16, 0))
	concert.BaseScore = 0.001
	pool := []domain.Candidate{
		place(1, domain.CategoryCulture, 0, north(origin, 100)),
		place(2, domain.CategorySport, 0, north(origin, 200)),
		place(3, domain.CategoryWalk, 0, north(origin, -200)),
	}
	requireInEveryRoute(t, search(t, wide, withAnchors(problem(), anchor(&concert)), pool), 9)
}

func TestSearchDropsVisitsThatMakeAnAnchorUnreachable(t *testing.T) {
	concert := session(9, domain.CategoryCulture, north(origin, 1000), at(10, 40), at(11, 30))
	concert.BaseScore = 0.001
	museum := place(1, domain.CategoryCulture, 0, north(origin, 100))
	for _, cfg := range []Config{wide, greedy} {
		routes := search(t, cfg, withAnchors(problem(), anchor(&concert)), []domain.Candidate{museum})
		requireInEveryRoute(t, routes, 9)
		for _, r := range routes {
			ids := placeIDs(r)
			if m := slices.Index(ids, 1); m >= 0 && m < slices.Index(ids, 9) {
				t.Fatalf("museum before the concert leaves no time to reach it: %v", ids)
			}
		}
	}
}

func TestSearchVisitsAnchorsInOrder(t *testing.T) {
	first := session(8, domain.CategoryCulture, north(origin, 500), at(11, 0), at(11, 30))
	second := session(9, domain.CategorySport, north(origin, -500), at(13, 0), at(13, 30))
	pool := []domain.Candidate{place(1, domain.CategoryWalk, 0, north(origin, 100))}
	routes := search(t, wide, withAnchors(problem(), anchor(&second), anchor(&first)), pool)
	requireInEveryRoute(t, routes, 8, 9)
	for _, r := range routes {
		if ids := placeIDs(r); slices.Index(ids, 8) > slices.Index(ids, 9) {
			t.Fatalf("anchors out of order: %v", ids)
		}
	}
}

func TestSearchAnchorReplacesPoolCandidate(t *testing.T) {
	concert := session(9, domain.CategoryCulture, north(origin, 500), at(11, 0), at(12, 0))
	buffered := withWindow(&concert, func(w *domain.VisitWindow) { w.ArrivalBuffer = 20 * time.Minute })
	routes := search(t, wide, withAnchors(problem(), anchor(&buffered)), []domain.Candidate{concert})
	requireInEveryRoute(t, routes, 9)
	for _, r := range routes {
		for _, v := range r.Visits {
			if v.Candidate.Place.ID[0] == 9 && v.Buffer != 20*time.Minute {
				t.Fatalf("anchor visit uses buffer %s", v.Buffer)
			}
		}
	}
}

func TestSearchSkipsVisitedPlaces(t *testing.T) {
	pool := []domain.Candidate{
		place(1, domain.CategoryCulture, 0, north(origin, 100)),
		place(2, domain.CategorySport, 0, north(origin, 200)),
	}
	p := problem()
	p.Visited = []domain.PlaceID{pool[0].Place.ID}
	routes := search(t, wide, p, pool)
	if len(routes) == 0 {
		t.Fatal("no routes")
	}
	for _, r := range routes {
		if slices.Contains(placeIDs(r), 1) {
			t.Fatalf("route %v returns to a visited place", placeIDs(r))
		}
	}
}

func TestSearchWithUnreachableAnchorFindsNothing(t *testing.T) {
	concert := session(9, domain.CategoryCulture, north(origin, 3000), at(10, 5), at(11, 0))
	routes := search(
		t,
		wide,
		withAnchors(problem(), anchor(&concert)),
		[]domain.Candidate{place(1, domain.CategoryWalk, 0, north(origin, 100))},
	)
	if len(routes) != 0 {
		t.Fatalf("routes without the anchor: %v", routeKeys(routes))
	}
}

func TestProblemRejectsInvalidAnchor(t *testing.T) {
	broken := session(9, domain.CategoryCulture, origin, at(11, 0), at(12, 0))
	broken.Place.ID = domain.PlaceID{}
	if err := withAnchors(problem(), anchor(&broken)).Validate(); err == nil {
		t.Fatal("invalid anchor accepted")
	}
}

func conflictCodes(conflicts []domain.Conflict) []string {
	codes := make([]string, len(conflicts))
	for i, c := range conflicts {
		codes[i] = c.Code
	}
	return codes
}

func TestAnchorsFor(t *testing.T) {
	concert := session(9, domain.CategoryCulture, origin, at(11, 0), at(12, 0))
	concert = withWindow(&concert, func(w *domain.VisitWindow) { w.ArrivalBuffer = 5 * time.Minute })
	cancelled := session(8, domain.CategoryCulture, origin, at(13, 0), at(14, 0))
	cancelled.Session.Availability = domain.AvailabilityCancelled
	soldOut := session(7, domain.CategoryCulture, origin, at(15, 0), at(16, 0))
	soldOut.Session.Availability = domain.AvailabilitySoldOut
	pool := []domain.Candidate{concert, cancelled, soldOut}

	longer := obligationFor(&concert)
	longer.ArrivalBuffer = 15 * time.Minute
	anchors, conflicts := AnchorsFor([]domain.Obligation{longer}, pool)
	if len(conflicts) != 0 || len(anchors) != 1 {
		t.Fatalf("anchors=%d conflicts=%v", len(anchors), conflictCodes(conflicts))
	}
	if a := anchors[0]; a.Candidate.Window.ArrivalBuffer != 15*time.Minute ||
		a.Candidate.Session.Window.ArrivalBuffer != 15*time.Minute {
		t.Fatalf("anchor buffer %s", a.Candidate.Window.ArrivalBuffer)
	}
	if pool[0].Window.ArrivalBuffer != 5*time.Minute {
		t.Fatal("anchor changed the pool candidate")
	}
	anchors, conflicts = AnchorsFor([]domain.Obligation{obligationFor(&concert)}, pool)
	if len(conflicts) != 0 || len(anchors) != 1 || anchors[0].Candidate.Window.ArrivalBuffer != 5*time.Minute {
		t.Fatalf("shorter obligation buffer: anchors=%v conflicts=%v", anchors, conflictCodes(conflicts))
	}
	if anchors, _ := AnchorsFor(
		[]domain.Obligation{obligationFor(&concert), obligationFor(&concert)},
		pool,
	); len(
		anchors,
	) != 1 {
		t.Fatalf("repeated obligation gave %d anchors", len(anchors))
	}

	visitOnly := domain.Obligation{VisitID: &domain.VisitID{4}, Participation: domain.ParticipationActionRequired}
	missing := domain.Obligation{SessionID: &domain.SessionID{42}, Participation: domain.ParticipationActionRequired}
	moved := obligationFor(&concert)
	moved.StartsAt = new(at(11, 30))
	unconfirmedSoldOut := obligationFor(&soldOut)
	unconfirmedSoldOut.Participation = domain.ParticipationActionRequired
	cases := map[string]struct {
		obligation domain.Obligation
		code       string
	}{
		"visit of another plan":       {visitOnly, "OBLIGATION_UNKNOWN"},
		"session not in the catalog":  {missing, "OBLIGATION_UNAVAILABLE"},
		"cancelled session":           {obligationFor(&cancelled), "OBLIGATION_CANCELLED"},
		"sold out without a ticket":   {unconfirmedSoldOut, "OBLIGATION_SOLD_OUT"},
		"session moved since pinning": {moved, "OBLIGATION_TIME_CHANGED"},
	}
	for name, tc := range cases {
		anchors, conflicts := AnchorsFor([]domain.Obligation{tc.obligation}, pool)
		if len(anchors) != 0 || !slices.Equal(conflictCodes(conflicts), []string{tc.code}) {
			t.Errorf("%s: anchors=%d conflicts=%v", name, len(anchors), conflictCodes(conflicts))
			continue
		}
		c := conflicts[0]
		if err := c.Validate(); err != nil || len(c.VisitIDs)+len(c.SessionIDs) == 0 {
			t.Errorf("%s: conflict %+v: %v", name, c, err)
		}
	}
	if anchors, conflicts := AnchorsFor(
		[]domain.Obligation{obligationFor(&soldOut)},
		pool,
	); len(anchors) != 1 ||
		len(conflicts) != 0 {
		t.Errorf("sold out with a ticket: anchors=%d conflicts=%v", len(anchors), conflictCodes(conflicts))
	}
	same := obligationFor(&concert)
	same.StartsAt = new(at(11, 0))
	if anchors, conflicts := AnchorsFor([]domain.Obligation{same}, pool); len(anchors) != 1 || len(conflicts) != 0 {
		t.Errorf("unchanged start: anchors=%d conflicts=%v", len(anchors), conflictCodes(conflicts))
	}
}

func TestDiagnose(t *testing.T) {
	s := newSolver(t, wide)
	fine := session(9, domain.CategoryCulture, north(origin, 500), at(11, 0), at(12, 0))
	tooSoon := session(8, domain.CategoryCulture, north(origin, 3000), at(10, 5), at(11, 0))
	tooLate := session(7, domain.CategoryCulture, north(origin, 500), at(17, 30), at(18, 30))
	overlap := session(6, domain.CategorySport, north(origin, -500), at(11, 30), at(12, 30))

	cases := []struct {
		name    string
		anchors []Anchor
		want    []string
	}{
		{"feasible anchor", []Anchor{anchor(&fine)}, nil},
		{"cannot arrive in time", []Anchor{anchor(&tooSoon)}, []string{"OBLIGATION_UNREACHABLE"}},
		{"ends after the day", []Anchor{anchor(&tooLate)}, []string{"OBLIGATION_UNREACHABLE"}},
		{"overlapping obligations", []Anchor{anchor(&fine), anchor(&overlap)}, []string{"OBLIGATIONS_OVERLAP"}},
	}
	for _, tc := range cases {
		conflicts, err := s.Diagnose(context.Background(), withAnchors(problem(), tc.anchors...))
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(conflictCodes(conflicts), tc.want) {
			t.Errorf("%s: conflicts %v", tc.name, conflictCodes(conflicts))
		}
		for _, c := range conflicts {
			if err := c.Validate(); err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
		}
	}
	conflicts, _ := s.Diagnose(context.Background(), withAnchors(problem(), anchor(&fine), anchor(&overlap)))
	if len(conflicts) != 1 || len(conflicts[0].SessionIDs) != 2 {
		t.Fatalf("overlap conflict %+v", conflicts)
	}
	expensive := priced(new(session(5, domain.CategoryCulture, north(origin, 500), at(15, 0), at(16, 0))), 90000)
	p := withAnchors(problem(), anchor(&expensive))
	p.Pricing.Budget = domain.Budget{
		Mode:  domain.BudgetStrict,
		Limit: &domain.Money{AmountMinor: 50000, Currency: "RUB"},
	}
	if conflicts, _ := s.Diagnose(
		context.Background(),
		p,
	); !slices.Equal(
		conflictCodes(conflicts),
		[]string{"OBLIGATION_OVER_BUDGET"},
	) {
		t.Fatalf("anchor over a strict budget: %v", conflictCodes(conflicts))
	}
}

// atVenue moves a session to another venue, keeping the candidate consistent.
func atVenue(c *domain.Candidate, venue byte) domain.Candidate {
	out := *c
	out.Place.ID = domain.PlaceID{venue}
	event := *c.Event
	event.PlaceID = out.Place.ID
	out.Event = &event
	return out
}

func requireSession(t *testing.T, routes []*domain.Branch, id byte) {
	t.Helper()
	if len(routes) == 0 {
		t.Fatal("no routes")
	}
	for _, r := range routes {
		if _, ok := r.UsedSessions[domain.SessionID{id}]; !ok {
			t.Fatalf("route %v misses anchored session %d", placeIDs(r), id)
		}
	}
}

func TestAnchorIsHonouredBySessionNotByVenue(t *testing.T) {
	evening := session(9, domain.CategoryCulture, north(origin, 500), at(16, 0), at(17, 0))
	evening.BaseScore = 0.001
	morning := atVenue(new(session(7, domain.CategoryCulture, north(origin, 500), at(11, 0), at(12, 0))), 9)
	for _, cfg := range []Config{wide, greedy} {
		routes := search(t, cfg, withAnchors(problem(), anchor(&evening)), []domain.Candidate{morning})
		requireSession(t, routes, 9)
		for _, r := range routes {
			if _, ok := r.UsedSessions[domain.SessionID{7}]; ok {
				t.Fatalf("route visits the anchored venue twice: %v", placeIDs(r))
			}
		}
	}
}

func TestAnchorAtPlaceVisitedEarlierToday(t *testing.T) {
	lecture := session(9, domain.CategoryCulture, north(origin, 500), at(16, 0), at(17, 0))
	p := withAnchors(problem(), anchor(&lecture))
	p.Visited = []domain.PlaceID{lecture.Place.ID}
	requireSession(t, search(t, wide, p, nil), 9)
	if conflicts, err := newSolver(t, wide).Diagnose(context.Background(), p); err != nil || len(conflicts) != 0 {
		t.Fatalf("conflicts=%v err=%v", conflictCodes(conflicts), err)
	}
}

func TestSearchWithAnOpenWindowAnchor(t *testing.T) {
	exhibition := withWindow(
		new(session(8, domain.CategoryCulture, north(origin, 300), at(10, 0), at(18, 0))),
		func(w *domain.VisitWindow) {
			w.Kind, w.MinDuration, w.RecommendedDuration = domain.WindowContinuous, time.Hour, 2*time.Hour
		},
	)
	lecture := session(9, domain.CategoryCulture, north(origin, 600), at(11, 0), at(12, 0))
	film := session(7, domain.CategoryCulture, north(origin, -400), at(13, 0), at(14, 0))
	routes := search(t, wide, withAnchors(problem(), anchor(&exhibition), anchor(&lecture), anchor(&film)), nil)
	requireInEveryRoute(t, routes, 7, 8, 9)
}

func TestProblemRequiresAnchorSession(t *testing.T) {
	if err := withAnchors(
		problem(),
		Anchor{Candidate: place(1, domain.CategoryCulture, 0, origin)},
	).Validate(); err == nil {
		t.Fatal("anchor without a session accepted")
	}
}

func TestAnchorWithinAStrictBudget(t *testing.T) {
	concert := priced(new(session(99, domain.CategoryCulture, north(origin, 500), at(16, 0), at(17, 0))), 80000)
	concert.BaseScore = 0.001
	var pool []domain.Candidate
	for i := range 24 {
		hour, minute := 10+i/4, (i%4)*15
		pool = append(
			pool,
			priced(
				new(session(
					byte(i+1),
					domain.CategoryCulture,
					north(origin, float64(50+10*i)),
					at(hour, minute),
					at(hour, minute+10),
				)),
				30000,
			),
		)
	}
	p := withAnchors(problem(), anchor(&concert))
	p.Pricing.Budget = domain.Budget{
		Mode:  domain.BudgetStrict,
		Limit: &domain.Money{AmountMinor: 100000, Currency: "RUB"},
	}
	for _, cfg := range []Config{greedy, wide} {
		requireSession(t, search(t, cfg, p, pool), 99)
	}
}

func TestFlexibleAnchorMustFitBetweenFixedOnes(t *testing.T) {
	lecture := session(90, domain.CategoryCulture, north(origin, 1500), at(11, 45), at(12, 30))
	exhibition := withWindow(
		new(session(91, domain.CategoryCulture, north(origin, -1500), at(10, 0), at(18, 0))),
		func(w *domain.VisitWindow) {
			w.Kind, w.MinDuration, w.RecommendedDuration = domain.WindowContinuous, 20*time.Minute, 30*time.Minute
			w.LastEntryAt = new(at(11, 30))
		},
	)
	var pool []domain.Candidate
	for i := range 20 {
		pool = append(pool, place(byte(i+1), domain.CategoryCulture, 0, north(origin, float64(10+5*i))))
	}
	p := withAnchors(problem(), anchor(&lecture), anchor(&exhibition))
	for _, cfg := range []Config{greedy, wide} {
		routes := search(t, cfg, p, pool)
		requireSession(t, routes, 90)
		requireSession(t, routes, 91)
	}
}

func TestTwoAnchorsAtOnePlace(t *testing.T) {
	morning := session(9, domain.CategoryCulture, north(origin, 500), at(11, 0), at(12, 0))
	evening := atVenue(new(session(8, domain.CategoryCulture, north(origin, 500), at(16, 0), at(17, 0))), 9)
	conflicts, err := newSolver(
		t,
		wide,
	).Diagnose(context.Background(), withAnchors(problem(), anchor(&morning), anchor(&evening)))
	if err != nil || !slices.Equal(conflictCodes(conflicts), []string{"OBLIGATIONS_SAME_PLACE"}) {
		t.Fatalf("conflicts=%v err=%v", conflictCodes(conflicts), err)
	}
}

func TestOpenWindowVisitShortensToKeepAnAnchor(t *testing.T) {
	exhibition := withWindow(
		new(session(8, domain.CategoryCulture, north(origin, 300), at(10, 0), at(18, 0))),
		func(w *domain.VisitWindow) {
			w.Kind, w.MinDuration, w.RecommendedDuration = domain.WindowContinuous, time.Hour, 2*time.Hour
		},
	)
	concert := session(9, domain.CategoryCulture, north(origin, 600), at(12, 0), at(13, 30))
	p := withAnchors(problem(), anchor(&exhibition), anchor(&concert))
	p.End = at(14, 0)
	for _, cfg := range []Config{greedy, wide} {
		routes := search(t, cfg, p, nil)
		requireSession(t, routes, 8)
		requireSession(t, routes, 9)
		for _, v := range routes[0].Visits {
			if v.Candidate.Session.ID == (domain.SessionID{8}) && v.EndAt.Sub(v.StartAt) < time.Hour {
				t.Fatalf("exhibition cut below its minimum: %s", v.EndAt.Sub(v.StartAt))
			}
		}
	}
}
