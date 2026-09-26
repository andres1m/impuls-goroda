package solver

import (
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func TestBasicPlacement(t *testing.T) {
	museum := place(1, domain.CategoryCulture, 0, origin)
	museum.Window.Start, museum.Window.End = at(10, 0), at(18, 0)
	shortWindow := museum
	shortWindow.Window.End = at(10, 30)
	shortWindow.Window.RecommendedDuration = 30 * time.Minute
	shortWindow.Window.MinDuration = 30 * time.Minute
	concert := session(2, domain.CategoryCulture, origin, at(15, 0), at(16, 30))
	early := problem()
	early.Start = at(9, 0)
	shortDay := problem()
	shortDay.End = at(10, 45)

	cases := []struct {
		name       string
		candidate  domain.Candidate
		arrival    time.Time
		p          Problem
		start, end time.Time
		ok         bool
	}{
		{"waits for opening", museum, at(9, 30), early, at(10, 0), at(11, 0), true},
		{"starts on arrival", museum, at(12, 5), problem(), at(12, 5), at(13, 5), true},
		{"does not fit the window", museum, at(17, 30), problem(), time.Time{}, time.Time{}, false},
		{"recommended duration longer than the rest of the window", shortWindow, at(10, 10), early, time.Time{}, time.Time{}, false},
		{"ends after the user's day", museum, at(10, 0), shortDay, time.Time{}, time.Time{}, false},
		{"fixed session keeps its start", concert, at(14, 50), problem(), at(15, 0), at(16, 30), true},
		{"arrives exactly at session start", concert, at(15, 0), problem(), at(15, 0), at(16, 30), true},
		{"late for a fixed session", concert, at(15, 1), problem(), time.Time{}, time.Time{}, false},
	}
	for _, tc := range cases {
		start, end, ok := BasicPlacement{}.Place(&tc.candidate, tc.arrival, tc.p)
		if ok != tc.ok || !start.Equal(tc.start) || !end.Equal(tc.end) {
			t.Fatalf("%s: got %s–%s ok=%v", tc.name, start, end, ok)
		}
	}
}
