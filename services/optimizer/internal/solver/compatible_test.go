package solver

import (
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func compatible(t *testing.T, a, b domain.Candidate) bool {
	t.Helper()
	ok, err := newSolver(t, wide).Compatible(problem(), &a, &b)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestCompatibleOverlappingSessions(t *testing.T) {
	first := session(1, domain.CategoryCulture, origin, at(15, 0), at(16, 30))
	second := session(2, domain.CategoryCulture, origin, at(16, 0), at(17, 0))
	if compatible(t, first, second) || compatible(t, second, first) {
		t.Fatal("overlapping sessions reported compatible")
	}
}

func TestCompatibleSequentialSessions(t *testing.T) {
	first := session(1, domain.CategoryCulture, origin, at(15, 0), at(16, 0))
	second := session(2, domain.CategoryCulture, north(origin, 1000), at(16, 30), at(17, 30))
	if !compatible(t, first, second) || !compatible(t, second, first) {
		t.Fatal("sequential sessions reported incompatible")
	}
}

func TestCompatibleCountsArrivalBuffer(t *testing.T) {
	first := session(1, domain.CategoryCulture, origin, at(15, 0), at(16, 0))
	second := withWindow(session(2, domain.CategoryCulture, north(origin, 1000), at(16, 30), at(17, 30)), func(w *domain.VisitWindow) {
		w.ArrivalBuffer = 15 * time.Minute
	})
	if compatible(t, first, second) {
		t.Fatal("the buffer before the second session cannot be kept")
	}
}

func TestCompatibleShortensContinuousVisit(t *testing.T) {
	museum := withWindow(place(1, domain.CategoryCulture, 0, origin), func(w *domain.VisitWindow) {
		w.Start, w.End = at(14, 0), at(15, 40)
		w.RecommendedDuration = 2 * time.Hour
	})
	concert := session(2, domain.CategoryCulture, origin, at(14, 40), at(15, 30))
	if !compatible(t, museum, concert) {
		t.Fatal("a minimal museum visit fits before the concert")
	}
}

func TestCompatibleRejectsInvalidInput(t *testing.T) {
	s := newSolver(t, wide)
	valid := place(1, domain.CategoryCulture, 0, origin)
	broken := place(2, domain.CategoryCulture, 0, origin)
	broken.Place.ID = domain.PlaceID{}
	bad := problem()
	bad.End = bad.Start
	if _, err := s.Compatible(bad, &valid, &valid); err == nil {
		t.Fatal("invalid problem accepted")
	}
	if _, err := s.Compatible(problem(), &valid, &broken); err == nil {
		t.Fatal("invalid candidate accepted")
	}
}
