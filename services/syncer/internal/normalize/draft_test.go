package normalize

import (
	"math"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)

func TestInHorizon(t *testing.T) {
	for name, tc := range map[string]struct {
		start, end time.Time
		want       bool
	}{
		"ended before now":        {now.Add(-2 * time.Hour), now, false},
		"runs across now":         {now.Add(-time.Hour), now.Add(time.Hour), true},
		"starts at horizon end":   {now.Add(SessionHorizon), now.Add(SessionHorizon + time.Hour), false},
		"starts before horizon":   {now.Add(SessionHorizon - time.Minute), now.Add(SessionHorizon + time.Hour), true},
		"shorter than one minute": {now.Add(time.Hour), now.Add(time.Hour + 59*time.Second), false},
		"end before start":        {now.Add(2 * time.Hour), now.Add(time.Hour), false},
	} {
		if got := inHorizon(tc.start, tc.end, now); got != tc.want {
			t.Errorf("%s: got %v", name, got)
		}
	}
}

func TestNewSessionSplitsPerformancesFromWindows(t *testing.T) {
	url := "https://example.test/buy"
	fixed := newSession(now, now.Add(4*time.Hour), fixedPrice(500), &url)
	if fixed.SlotType != "FIXED_SESSION" || fixed.MinDuration != 4*time.Hour || fixed.RecommendedDuration != 4*time.Hour ||
		fixed.AccessType != "ticket" || fixed.BookingURL != &url {
		t.Fatalf("fixed %+v", fixed)
	}
	window := newSession(now, now.Add(4*time.Hour+time.Minute), freePrice(), nil)
	if window.SlotType != "CONTINUOUS_WINDOW" || window.MinDuration != 30*time.Minute || window.RecommendedDuration != time.Hour ||
		window.AccessType != "free" {
		t.Fatalf("window %+v", window)
	}
	local := newSession(now.In(time.FixedZone("MSK", 3*3600)), now.Add(time.Hour), unknownPrice(""), nil)
	if local.StartsAt.Location() != time.UTC {
		t.Fatalf("start kept zone %v", local.StartsAt.Location())
	}
}

func TestAddSessionKeepsTheFirstOfOneStart(t *testing.T) {
	first := newSession(now, now.Add(time.Hour), fixedPrice(1), nil)
	second := newSession(now, now.Add(2*time.Hour), fixedPrice(2), nil)
	got := addSession(addSession(nil, first), second)
	if len(got) != 1 || *got[0].Price.AmountMin != 100 {
		t.Fatalf("sessions %+v", got)
	}
}

func TestPrices(t *testing.T) {
	if p := freePrice(); p.Status != "free" || *p.AmountMin != 0 || *p.AmountMax != 0 {
		t.Fatalf("free %+v", p)
	}
	if p := rangePrice(100, 300); p.Status != "range" || *p.AmountMin != 10000 || *p.AmountMax != 30000 {
		t.Fatalf("range is kept in kopecks: %+v", p)
	}
	if p := fixedPrice(500); p.Status != "fixed" || *p.AmountMin != 50000 || *p.AmountMax != 50000 {
		t.Fatalf("fixed is kept in kopecks: %+v", p)
	}
	if p := fixedPrice(math.MaxInt64 / 10); p.Status != "unknown" || p.AmountMin != nil {
		t.Fatalf("a sum that overflows kopecks: %+v", p)
	}
	if p := rangePrice(1, math.MaxInt64/10); p.Status != "unknown" {
		t.Fatalf("a range that overflows kopecks: %+v", p)
	}
	if p := unknownPrice(""); p.Status != "unknown" || p.AmountMin != nil || p.TariffLabel != nil {
		t.Fatalf("unknown %+v", p)
	}
	if p := unknownPrice("от 600 ₽"); *p.TariffLabel != "от 600 ₽" {
		t.Fatalf("labelled %+v", p)
	}
}

func TestIDsAreStable(t *testing.T) {
	event := EntityID("mkrf_events:event:1@place:2")
	session := SessionID(event, now.In(time.FixedZone("MSK", 3*3600)))
	if session != SessionID(event, now) || PriceID(session) != PriceID(SessionID(event, now)) {
		t.Fatal("ids depend on the zone of the start")
	}
	if SessionID(event, now) == SessionID(event, now.Add(time.Second)) || event == EntityID("mkrf_events:event:1@place:3") {
		t.Fatal("different entities share an id")
	}
}

func TestUniqueSorted(t *testing.T) {
	got := uniqueSorted([]string{"b", "a", "b"})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %v", got)
	}
	if uniqueSorted(nil) != nil {
		t.Fatal("empty input gives a slice")
	}
}
