package normalize

import (
	"math"
	"slices"
	"time"

	"github.com/google/uuid"
)

// Draft is what one raw record becomes in the catalog: a place, and an event at it when the source describes events.
type Draft struct {
	Place PlaceDraft
	Event *EventDraft
}

type EventDraft struct {
	// ExternalID joins the source event id with its place's, so an event that moves becomes a new row
	// instead of rewriting the place of one that saved routes point to.
	ExternalID      string
	Title           string
	NormalizedTitle string
	Category        string
	Tags            []string
	Organizer       *string
	AgeMin          *int16
	Sessions        []SessionDraft
}

type SessionDraft struct {
	StartsAt, EndsAt    time.Time
	SlotType            string
	MinDuration         time.Duration
	RecommendedDuration time.Duration
	AccessType          string
	BookingURL          *string
	Price               PriceDraft
}

type PriceDraft struct {
	Status      string
	AmountMin   *int64
	AmountMax   *int64
	TariffLabel *string
}

// SessionHorizon bounds how far ahead sessions are materialized: the optimizer loads every future session.
const SessionHorizon = 14 * 24 * time.Hour

// A slot this long states no real visiting hours: sources use it for "open some time during these days"
// or for runs spanning years, and the place's own hours are unknown.
const longestSession = 23 * time.Hour

const (
	longestPerformance        = 4 * time.Hour
	windowMinDuration         = 30 * time.Minute
	windowRecommendedDuration = time.Hour
)

func SessionID(eventID uuid.UUID, start time.Time) uuid.UUID {
	return EntityID(eventID.String() + ":session:" + start.UTC().Format(time.RFC3339))
}

func PriceID(sessionID uuid.UUID) uuid.UUID {
	return EntityID(sessionID.String() + ":price:general")
}

func inHorizon(start, end, now time.Time) bool {
	d := end.Sub(start)
	return d >= time.Minute && d < longestSession && end.After(now) && start.Before(now.Add(SessionHorizon))
}

// newSession treats a short slot as a performance attended whole and a long one as an opening window
// visited for part of it.
func newSession(start, end time.Time, price PriceDraft, bookingURL *string) SessionDraft {
	s := SessionDraft{StartsAt: start.UTC(), EndsAt: end.UTC(), AccessType: "ticket", BookingURL: bookingURL, Price: price}
	if price.Status == "free" {
		s.AccessType = "free"
	}
	if d := end.Sub(start); d <= longestPerformance {
		s.SlotType, s.MinDuration, s.RecommendedDuration = "FIXED_SESSION", d, d
	} else {
		s.SlotType, s.MinDuration, s.RecommendedDuration = "CONTINUOUS_WINDOW", windowMinDuration, windowRecommendedDuration
	}
	return s
}

func addSession(sessions []SessionDraft, s SessionDraft) []SessionDraft {
	for _, existing := range sessions {
		if existing.StartsAt.Equal(s.StartsAt) {
			return sessions
		}
	}
	return append(sessions, s)
}

func amount(v int64) *int64 { return &v }

const kopecksPerRuble = 100

// kopecks converts a source's ruble sum to the catalog's minor units.
func kopecks(rubles int64) (int64, bool) {
	if rubles < 0 || rubles > math.MaxInt64/kopecksPerRuble {
		return 0, false
	}
	return rubles * kopecksPerRuble, true
}

func freePrice() PriceDraft {
	return PriceDraft{Status: "free", AmountMin: amount(0), AmountMax: amount(0)}
}

func fixedPrice(rubles int64) PriceDraft {
	v, ok := kopecks(rubles)
	if !ok {
		return unknownPrice("")
	}
	return PriceDraft{Status: "fixed", AmountMin: amount(v), AmountMax: amount(v)}
}

func rangePrice(loRubles, hiRubles int64) PriceDraft {
	lo, okLo := kopecks(loRubles)
	hi, okHi := kopecks(hiRubles)
	if !okLo || !okHi {
		return unknownPrice("")
	}
	return PriceDraft{Status: "range", AmountMin: amount(lo), AmountMax: amount(hi)}
}

func unknownPrice(label string) PriceDraft {
	return PriceDraft{Status: "unknown", TariffLabel: optional(label)}
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func uniqueSorted(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := slices.Clone(values)
	slices.Sort(out)
	return slices.Compact(out)
}
