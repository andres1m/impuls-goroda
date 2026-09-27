package solver

import (
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func withWindow(c domain.Candidate, change func(w *domain.VisitWindow)) domain.Candidate {
	change(&c.Window)
	if c.Session != nil {
		session := *c.Session
		session.Window = c.Window
		c.Session = &session
	}
	return c
}

func ptr[T any](v T) *T {
	return &v
}

func TestWindowPlacement(t *testing.T) {
	museum := withWindow(place(1, domain.CategoryCulture, 0, origin), func(w *domain.VisitWindow) {
		w.Start, w.End = at(10, 0), at(18, 0)
	})
	buffered := withWindow(museum, func(w *domain.VisitWindow) { w.ArrivalBuffer = 10 * time.Minute })
	hourMinimum := withWindow(museum, func(w *domain.VisitWindow) { w.MinDuration = time.Hour })
	lastEntry := withWindow(museum, func(w *domain.VisitWindow) { w.LastEntryAt = ptr(at(17, 0)) })

	concert := session(2, domain.CategoryCulture, origin, at(15, 0), at(16, 30))
	bufferedConcert := withWindow(concert, func(w *domain.VisitWindow) { w.ArrivalBuffer = 15 * time.Minute })
	lateDenied := withWindow(concert, func(w *domain.VisitWindow) { w.LateEntryAllowed = ptr(false) })
	lateAllowed := withWindow(concert, func(w *domain.VisitWindow) {
		w.LateEntryAllowed = ptr(true)
		w.MinDuration = 30 * time.Minute
		w.ArrivalBuffer = 15 * time.Minute
	})
	lateUntil := withWindow(lateAllowed, func(w *domain.VisitWindow) { w.LastEntryAt = ptr(at(15, 15)) })
	unknownKind := withWindow(museum, func(w *domain.VisitWindow) { w.Kind = "flexible" })

	dayEnd := at(18, 0)
	cases := []struct {
		name      string
		candidate domain.Candidate
		arrival   time.Time
		deadline  time.Time
		want      Slot
		ok        bool
	}{
		{"waits for opening", museum, at(9, 30), dayEnd, Slot{at(10, 0), at(11, 0), 0}, true},
		{"starts on arrival", museum, at(12, 5), dayEnd, Slot{at(12, 5), at(13, 5), 0}, true},
		{"buffer delays the start", buffered, at(12, 0), dayEnd, Slot{at(12, 10), at(13, 10), 10 * time.Minute}, true},
		{"buffer passes while waiting for opening", buffered, at(9, 30), dayEnd, Slot{at(10, 0), at(11, 0), 10 * time.Minute}, true},
		{"shortened to the rest of the window", museum, at(17, 20), dayEnd, Slot{at(17, 20), at(18, 0), 0}, true},
		{"visit longer than the rest of the window", hourMinimum, at(17, 50), dayEnd, Slot{}, false},
		{"minimum longer than the rest of the window", museum, at(17, 40), dayEnd, Slot{}, false},
		{"shortened to the deadline", museum, at(12, 0), at(12, 45), Slot{at(12, 0), at(12, 45), 0}, true},
		{"deadline leaves less than the minimum", museum, at(12, 0), at(12, 20), Slot{}, false},
		{"enters before last entry", lastEntry, at(16, 50), dayEnd, Slot{at(16, 50), at(17, 50), 0}, true},
		{"enters at last entry", lastEntry, at(17, 0), dayEnd, Slot{at(17, 0), at(18, 0), 0}, true},
		{"arrives after last entry", lastEntry, at(17, 1), dayEnd, Slot{}, false},

		{"fixed session keeps its start", concert, at(14, 50), dayEnd, Slot{at(15, 0), at(16, 30), 0}, true},
		{"arrives exactly at session start", concert, at(15, 0), dayEnd, Slot{at(15, 0), at(16, 30), 0}, true},
		{"late without confirmed late entry", concert, at(15, 1), dayEnd, Slot{}, false},
		{"late when late entry is denied", lateDenied, at(15, 20), dayEnd, Slot{}, false},
		{"early arrival keeps buffer apart from waiting", bufferedConcert, at(14, 30), dayEnd, Slot{at(15, 0), at(16, 30), 15 * time.Minute}, true},
		{"arrives with exactly the buffer", bufferedConcert, at(14, 45), dayEnd, Slot{at(15, 0), at(16, 30), 15 * time.Minute}, true},
		{"arrives without the full buffer", bufferedConcert, at(14, 50), dayEnd, Slot{}, false},
		{"late entry starts on arrival", lateAllowed, at(15, 20), dayEnd, Slot{at(15, 20), at(16, 30), 0}, true},
		{"late entry without the full buffer starts on time", lateAllowed, at(14, 50), dayEnd, Slot{at(15, 0), at(16, 30), 0}, true},
		{"late entry after last entry", lateUntil, at(15, 20), dayEnd, Slot{}, false},
		{"late entry at last entry", lateUntil, at(15, 15), dayEnd, Slot{at(15, 15), at(16, 30), 0}, true},
		{"late entry leaves exactly the minimum", lateAllowed, at(16, 0), dayEnd, Slot{at(16, 0), at(16, 30), 0}, true},
		{"late entry leaves less than the minimum", lateAllowed, at(16, 1), dayEnd, Slot{}, false},
		{"session ends after the deadline", concert, at(14, 0), at(16, 0), Slot{}, false},

		{"unknown window kind", unknownKind, at(12, 0), dayEnd, Slot{}, false},
	}
	for _, tc := range cases {
		got, ok := WindowPlacement{}.Place(&tc.candidate, tc.arrival, tc.deadline)
		if ok != tc.ok || !got.StartAt.Equal(tc.want.StartAt) || !got.EndAt.Equal(tc.want.EndAt) || got.Buffer != tc.want.Buffer {
			t.Errorf("%s: got %s–%s buffer %s ok=%v", tc.name, got.StartAt.Format("15:04"), got.EndAt.Format("15:04"), got.Buffer, ok)
		}
	}
}
