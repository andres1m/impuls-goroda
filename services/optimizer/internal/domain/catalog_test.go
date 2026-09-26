package domain

import (
	"math"
	"testing"
	"time"
)

var (
	day      = time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	fetched  = day.Add(-time.Hour)
	provided = Provenance{SourceName: "kudago", FetchedAt: fetched}
	rub      = "RUB"
)

func at(hour, minute int) time.Time {
	return day.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

func timePtr(t time.Time) *time.Time { return &t }

func intPtr(v int) *int { return &v }

func validWindow() VisitWindow {
	return VisitWindow{
		Kind:                WindowContinuous,
		Start:               at(10, 0),
		End:                 at(18, 0),
		MinDuration:         time.Hour,
		RecommendedDuration: 90 * time.Minute,
	}
}

func TestVisitWindowValidate(t *testing.T) {
	fixed := validWindow()
	fixed.Kind = WindowFixed
	fixed.End = at(11, 0)
	fixed.RecommendedDuration = time.Hour
	fixed.ArrivalBuffer = 15 * time.Minute

	tests := []struct {
		name   string
		mutate func(*VisitWindow)
		ok     bool
	}{
		{"continuous", func(*VisitWindow) {}, true},
		{"fixed", func(w *VisitWindow) { *w = fixed }, true},
		{"zero start", func(w *VisitWindow) { w.Start = time.Time{} }, false},
		{"end not after start", func(w *VisitWindow) { w.End = w.Start }, false},
		{"no minimum", func(w *VisitWindow) { w.MinDuration = 0 }, false},
		{"recommended below minimum", func(w *VisitWindow) { w.RecommendedDuration = 30 * time.Minute }, false},
		{"negative buffer", func(w *VisitWindow) { w.ArrivalBuffer = -time.Minute }, false},
		{"last entry after end", func(w *VisitWindow) { w.LastEntryAt = timePtr(at(19, 0)) }, false},
		{"last entry before start", func(w *VisitWindow) { w.LastEntryAt = timePtr(at(9, 0)) }, false},
		{"last entry inside", func(w *VisitWindow) { w.LastEntryAt = timePtr(at(17, 0)) }, true},
		{"continuous shorter than minimum", func(w *VisitWindow) { w.End = at(10, 30) }, false},
		{"invalid kind", func(w *VisitWindow) { w.Kind = "open" }, false},
		{"fixed end equals start", func(w *VisitWindow) { *w = fixed; w.End = w.Start }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := validWindow()
			tt.mutate(&w)
			if err := w.Validate(); (err == nil) != tt.ok {
				t.Fatalf("Validate() = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

func culture() *Category {
	c := CategoryCulture
	return &c
}

func validPlace() Place {
	return Place{
		ID:           PlaceID{1},
		City:         "moscow",
		Title:        "Museum",
		Category:     culture(),
		InterestMask: Interests(InterestClassicalArt),
		Location:     Coordinate{Longitude: 37.6, Latitude: 55.7},
		DataMode:     DataLive,
		Provenance:   provided,
	}
}

func validEvent() Event {
	return Event{
		ID:           EventID{2},
		PlaceID:      PlaceID{1},
		Title:        "Exhibition",
		Category:     CategoryCulture,
		InterestMask: Interests(InterestContemporaryArt),
		DataMode:     DataLive,
		Provenance:   provided,
	}
}

func validSession() Session {
	return Session{
		ID:           SessionID{3},
		EventID:      EventID{2},
		Window:       validWindow(),
		Access:       AccessTicket,
		Availability: AvailabilityAvailable,
		Version:      1,
		DataMode:     DataLive,
		Provenance:   provided,
	}
}

func validOffer() PriceOffer {
	return PriceOffer{
		ID:         PriceOfferID{4},
		SessionID:  SessionID{3},
		Price:      Price{Status: PriceFixed, Currency: rub, LowerMinor: i64(50000), UpperMinor: i64(50000)},
		Audience:   AudienceGeneral,
		Provenance: provided,
	}
}

func validEntrance() Entrance {
	return Entrance{
		ID:            EntranceID{5},
		PlaceID:       PlaceID{1},
		Location:      Coordinate{Longitude: 37.6, Latitude: 55.7},
		AllowedModes:  []MovementMode{"walk"},
		Accessibility: AccessibilityUnknown,
		Verification:  VerificationEstimated,
	}
}

func validEventCandidate() Candidate {
	event, session := validEvent(), validSession()
	return Candidate{
		Place:     validPlace(),
		Event:     &event,
		Session:   &session,
		Window:    session.Window,
		Entrances: []Entrance{validEntrance()},
		Offers:    []PriceOffer{validOffer()},
		BaseScore: 1,
	}
}

func TestCatalogEntitiesValidate(t *testing.T) {
	tests := []struct {
		name string
		err  error
		ok   bool
	}{
		{"place", validPlace().Validate(), true},
		{"place without category", func() error { p := validPlace(); p.Category = nil; return p.Validate() }(), true},
		{"place blank city", func() error { p := validPlace(); p.City = " "; return p.Validate() }(), false},
		{"place blank title", func() error { p := validPlace(); p.Title = ""; return p.Validate() }(), false},
		{"place unknown category", func() error { p := validPlace(); c := Category("bar"); p.Category = &c; return p.Validate() }(), false},
		{"event blank title", func() error { e := validEvent(); e.Title = ""; return e.Validate() }(), false},
		{"event negative max age", func() error { e := validEvent(); e.AgeMax = intPtr(-1); return e.Validate() }(), false},
		{"place zero id", func() error { p := validPlace(); p.ID = PlaceID{}; return p.Validate() }(), false},
		{"place bad location", func() error { p := validPlace(); p.Location.Latitude = math.NaN(); return p.Validate() }(), false},
		{"event", validEvent().Validate(), true},
		{"event age inverted", func() error { e := validEvent(); e.AgeMin, e.AgeMax = intPtr(10), intPtr(5); return e.Validate() }(), false},
		{"event negative age", func() error { e := validEvent(); e.AgeMin = intPtr(-1); return e.Validate() }(), false},
		{"session", validSession().Validate(), true},
		{"session zero version", func() error { s := validSession(); s.Version = 0; return s.Validate() }(), false},
		{"session zero registration deadline", func() error { s := validSession(); s.RegistrationDeadline = &time.Time{}; return s.Validate() }(), false},
		{"session zero availability time", func() error { s := validSession(); s.AvailabilityObservedAt = &time.Time{}; return s.Validate() }(), false},
		{"offer zero validity", func() error { o := validOffer(); o.ValidUntil = &time.Time{}; return o.Validate() }(), false},
		{"offer blank benefit program", func() error { o := validOffer(); o.BenefitPrograms = []string{" "}; return o.Validate() }(), false},
		{"session bad access", func() error { s := validSession(); s.Access = "vip"; return s.Validate() }(), false},
		{"offer", validOffer().Validate(), true},
		{"offer empty audience", func() error { o := validOffer(); o.Audience = ""; return o.Validate() }(), false},
		{"offer eligibility inverted", func() error {
			o := validOffer()
			o.EligibilityAgeMin, o.EligibilityAgeMax = intPtr(18), intPtr(14)
			return o.Validate()
		}(), false},
		{"entrance", validEntrance().Validate(), true},
		{"entrance bad accessibility", func() error { e := validEntrance(); e.Accessibility = "yes"; return e.Validate() }(), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if (tt.err == nil) != tt.ok {
				t.Fatalf("Validate() = %v, want ok=%v", tt.err, tt.ok)
			}
		})
	}
}

func TestCandidateValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Candidate)
		ok     bool
	}{
		{"event visit", func(*Candidate) {}, true},
		{"place visit", func(c *Candidate) { c.Event, c.Session, c.Offers = nil, nil, nil }, true},
		{"place visit without category", func(c *Candidate) { c.Event, c.Session, c.Offers, c.Place.Category = nil, nil, nil, nil }, false},
		{"place visit with price offers", func(c *Candidate) { c.Event, c.Session = nil, nil }, false},
		{"event without session", func(c *Candidate) { c.Session = nil }, false},
		{"session without event", func(c *Candidate) { c.Event = nil }, false},
		{"event of another place", func(c *Candidate) { c.Event.PlaceID = PlaceID{9} }, false},
		{"session of another event", func(c *Candidate) { c.Session.EventID = EventID{9} }, false},
		{"offer of another session", func(c *Candidate) { c.Offers[0].SessionID = SessionID{9} }, false},
		{"entrance of another place", func(c *Candidate) { c.Entrances[0].PlaceID = PlaceID{9} }, false},
		{"score NaN", func(c *Candidate) { c.BaseScore = math.NaN() }, false},
		{"score negative", func(c *Candidate) { c.BaseScore = -1 }, false},
		{"invalid window", func(c *Candidate) { c.Window.MinDuration = 0 }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validEventCandidate()
			tt.mutate(&c)
			if err := c.Validate(); (err == nil) != tt.ok {
				t.Fatalf("Validate() = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

func TestCandidateVisitAttributes(t *testing.T) {
	c := validEventCandidate()
	c.Event.Category = CategoryTourism
	if c.Category() != CategoryTourism || c.InterestMask() != Interests(InterestContemporaryArt) {
		t.Fatalf("event visit uses %s/%#x, want the event's category and mask", c.Category(), c.InterestMask())
	}

	c.Event, c.Session, c.Offers = nil, nil, nil
	if c.Category() != CategoryCulture || c.InterestMask() != Interests(InterestClassicalArt) {
		t.Fatalf("place visit uses %s/%#x, want the place's category and mask", c.Category(), c.InterestMask())
	}

	c = validEventCandidate()
	c.Event.DataMode = DataPrepared
	if got := c.DataMode(); got != DataPrepared {
		t.Fatalf("DataMode() = %s, want prepared", got)
	}

	c = validEventCandidate()
	c.Session.DataMode = DataSynthetic
	if got := c.DataMode(); got != DataSynthetic {
		t.Fatalf("DataMode() = %s, want synthetic", got)
	}
}
