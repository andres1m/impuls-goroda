package solver

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func visitStep(c domain.Candidate) RepairStep {
	return RepairStep{Candidate: &c}
}

func repair(t *testing.T, p Problem, steps ...RepairStep) Repair {
	t.Helper()
	r, err := newSolver(t, wide).Repair(context.Background(), p, steps)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func keptSteps(r Repair) []int {
	out := make([]int, len(r.Stops))
	for i, s := range r.Stops {
		out[i] = s.Step
	}
	return out
}

func TestRepairShiftsTheRestAfterADelay(t *testing.T) {
	museum := place(1, domain.CategoryCulture, 0, north(origin, 300))
	park := place(2, domain.CategoryWalk, 0, north(origin, 600))
	p := problem()
	p.Start = at(12, 0)
	r := repair(t, p, visitStep(museum), visitStep(park))
	if !slices.Equal(keptSteps(r), []int{0, 1}) || len(r.Dropped) != 0 || r.Unreachable != nil {
		t.Fatalf("kept %v dropped %v", keptSteps(r), r.Dropped)
	}
	first := r.Stops[0].Visit
	if !first.ArrivalAt.Equal(at(12, 5)) || !first.StartAt.Equal(at(12, 5)) || first.EndAt.Sub(first.StartAt) != time.Hour {
		t.Fatalf("first visit %s–%s", first.StartAt.Format("15:04"), first.EndAt.Format("15:04"))
	}
	if r.Stops[1].Visit.StartAt.Before(first.EndAt) {
		t.Fatal("second visit overlaps the first")
	}
}

func TestRepairShortensButNeverBelowTheMinimum(t *testing.T) {
	museum := place(1, domain.CategoryCulture, 0, north(origin, 300))
	p := problem()
	p.Start, p.End = at(17, 0), at(17, 50)
	r := repair(t, p, visitStep(museum))
	if len(r.Stops) != 1 || r.Stops[0].Visit.EndAt.Sub(r.Stops[0].Visit.StartAt) != 45*time.Minute {
		t.Fatalf("stops %+v", r.Stops)
	}
	p.End = at(17, 30)
	if r := repair(t, p, visitStep(museum)); len(r.Stops) != 0 || !slices.Equal(r.Dropped, []int{0}) {
		t.Fatalf("visit squeezed below its minimum: kept %v dropped %v", keptSteps(r), r.Dropped)
	}
}

func TestRepairDropsASoftVisitToKeepAnObligation(t *testing.T) {
	museum := place(1, domain.CategoryCulture, 0, north(origin, 300))
	concert := session(9, domain.CategoryCulture, north(origin, 600), at(13, 0), at(14, 0))
	p := withAnchors(problem(), anchor(concert))
	p.Start = at(12, 40)
	r := repair(t, p, visitStep(museum), visitStep(concert))
	if !slices.Equal(keptSteps(r), []int{1}) || !slices.Equal(r.Dropped, []int{0}) || r.Unreachable != nil {
		t.Fatalf("kept %v dropped %v unreachable %v", keptSteps(r), r.Dropped, r.Unreachable)
	}
}

func TestRepairReportsAnUnreachableObligation(t *testing.T) {
	concert := session(9, domain.CategoryCulture, north(origin, 600), at(13, 0), at(14, 0))
	p := withAnchors(problem(), anchor(concert))
	p.Start = at(13, 5)
	r := repair(t, p, visitStep(concert))
	if r.Unreachable == nil || r.Unreachable.Candidate.Session.ID != concert.Session.ID {
		t.Fatalf("unreachable %v, kept %v", r.Unreachable, keptSteps(r))
	}
}

func TestRepairKeepsAPause(t *testing.T) {
	museum := place(1, domain.CategoryCulture, 0, north(origin, 300))
	pause := RepairStep{Pause: 30 * time.Minute, NotBefore: at(12, 0)}
	p := problem()
	p.Start = at(10, 30)
	r := repair(t, p, visitStep(museum), pause)
	if !slices.Equal(keptSteps(r), []int{0, 1}) {
		t.Fatalf("kept %v dropped %v", keptSteps(r), r.Dropped)
	}
	if stop := r.Stops[1]; stop.Visit != nil || !stop.PauseStart.Equal(at(12, 0)) || !stop.PauseEnd.Equal(at(12, 30)) {
		t.Fatalf("pause %+v", stop)
	}
	late := problem()
	late.Start = at(12, 20)
	if r := repair(t, late, pause); len(r.Stops) != 1 || !r.Stops[0].PauseStart.Equal(at(12, 20)) {
		t.Fatalf("late pause %+v", r.Stops)
	}
}

func TestRepairDropsAPauseThatWouldCostAnObligation(t *testing.T) {
	concert := session(9, domain.CategoryCulture, north(origin, 600), at(13, 0), at(14, 0))
	p := withAnchors(problem(), anchor(concert))
	p.Start = at(12, 30)
	r := repair(t, p, RepairStep{Pause: 30 * time.Minute}, visitStep(concert))
	if !slices.Equal(keptSteps(r), []int{1}) || !slices.Equal(r.Dropped, []int{0}) {
		t.Fatalf("kept %v dropped %v", keptSteps(r), r.Dropped)
	}
}

func TestRepairKeepsPlannedTimes(t *testing.T) {
	museum := place(1, domain.CategoryCulture, 0, north(origin, 300))
	step := visitStep(museum)
	step.NotBefore = at(13, 0)
	r := repair(t, problem(), step)
	if len(r.Stops) != 1 || !r.Stops[0].Visit.StartAt.Equal(at(13, 0)) || !r.Stops[0].Visit.ArrivalAt.Equal(at(10, 5)) {
		t.Fatalf("stops %+v", r.Stops)
	}
}

func TestRepairReachesTheDestination(t *testing.T) {
	museum := place(1, domain.CategoryCulture, 0, north(origin, 300))
	p := problem()
	p.Destination = &origin
	p.End = at(11, 10)
	r := repair(t, p, visitStep(museum))
	if len(r.Stops) != 1 || r.Finish == nil || r.Stops[0].Visit.EndAt.Add(r.Finish.Duration).After(p.End) {
		t.Fatalf("stops %+v finish %+v", r.Stops, r.Finish)
	}
}

func TestRepairShortensAVisitToKeepAnObligation(t *testing.T) {
	museum := place(1, domain.CategoryCulture, 0, north(origin, 300))
	concert := session(9, domain.CategoryCulture, north(origin, 600), at(13, 0), at(14, 0))
	p := withAnchors(problem(), anchor(concert))
	p.Start = at(12, 0)
	r := repair(t, p, visitStep(museum), visitStep(concert))
	if !slices.Equal(keptSteps(r), []int{0, 1}) {
		t.Fatalf("kept %v dropped %v", keptSteps(r), r.Dropped)
	}
	if v := r.Stops[0].Visit; v.EndAt.Sub(v.StartAt) != 30*time.Minute {
		t.Fatalf("museum stays %s", v.EndAt.Sub(v.StartAt))
	}
}

func TestRepairNeedsAWayToTheDestination(t *testing.T) {
	museum := place(1, domain.CategoryCulture, 0, north(origin, 300))
	far := north(origin, 50000)
	p := problem()
	p.Start, p.Destination = at(17, 0), &far
	r := repair(t, p, visitStep(museum))
	if !r.DestinationUnreachable || len(r.Stops) != 0 {
		t.Fatalf("repair %+v", r)
	}
	if r := repair(t, p); !r.DestinationUnreachable {
		t.Fatal("empty repair claims the destination is reachable")
	}
	near := north(origin, 100)
	p.Destination = &near
	if r := repair(t, p); r.DestinationUnreachable || r.Finish == nil {
		t.Fatalf("repair without steps: %+v", r)
	}
}

func TestRepairKeepsTheObligationBuffer(t *testing.T) {
	concert := session(9, domain.CategoryCulture, north(origin, 600), at(13, 0), at(14, 0))
	pinned := withWindow(concert, func(w *domain.VisitWindow) { w.ArrivalBuffer = 20 * time.Minute })
	p := withAnchors(problem(), anchor(pinned))
	p.Start = at(12, 50)
	if r := repair(t, p, visitStep(concert)); r.Unreachable == nil {
		t.Fatalf("obligation buffer ignored: %+v", r.Stops)
	}
	p.Start = at(12, 20)
	r := repair(t, p, visitStep(concert))
	if len(r.Stops) != 1 || r.Stops[0].Visit.Buffer != 20*time.Minute {
		t.Fatalf("stops %+v", r.Stops)
	}
}

func TestRepairKeepsAPlannedLateEntry(t *testing.T) {
	talk := withWindow(session(9, domain.CategoryCulture, north(origin, 300), at(13, 0), at(15, 0)), func(w *domain.VisitWindow) {
		w.LateEntryAllowed, w.MinDuration, w.ArrivalBuffer = ptr(true), 30*time.Minute, 15*time.Minute
	})
	step := visitStep(talk)
	step.NotBefore = at(13, 30)
	p := problem()
	p.Start = at(13, 10)
	r := repair(t, p, step)
	if len(r.Stops) != 1 || !r.Stops[0].Visit.StartAt.Equal(at(13, 30)) {
		t.Fatalf("stops %+v", r.Stops)
	}
}

func TestRepairWaitsOutTheBufferBeforeAPlannedStart(t *testing.T) {
	museum := withWindow(place(1, domain.CategoryCulture, 0, north(origin, 300)), func(w *domain.VisitWindow) { w.ArrivalBuffer = 10 * time.Minute })
	step := visitStep(museum)
	step.NotBefore = at(13, 0)
	r := repair(t, problem(), step)
	if len(r.Stops) != 1 || !r.Stops[0].Visit.StartAt.Equal(at(13, 0)) {
		t.Fatalf("stops %+v", r.Stops)
	}
}

func TestRepairDropsAPauseThatDoesNotFit(t *testing.T) {
	p := problem()
	p.Start = at(17, 50)
	if r := repair(t, p, RepairStep{Pause: 30 * time.Minute}); !slices.Equal(r.Dropped, []int{0}) {
		t.Fatalf("pause past the day end: %+v", r)
	}
	far := north(origin, 3000)
	p.Start, p.Destination = at(17, 0), &far
	if r := repair(t, p, RepairStep{Pause: 50 * time.Minute}); !slices.Equal(r.Dropped, []int{0}) {
		t.Fatalf("pause that leaves no time to reach the destination: %+v", r)
	}
}

func TestRepairReportsAnObligationMissingFromTheSteps(t *testing.T) {
	concert := session(9, domain.CategoryCulture, north(origin, 600), at(13, 0), at(14, 0))
	if r := repair(t, withAnchors(problem(), anchor(concert))); r.Unreachable == nil {
		t.Fatal("repair lost an obligation it was never given")
	}
}
