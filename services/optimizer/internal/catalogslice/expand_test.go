package catalogslice

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/usecase"
)

var (
	perm, _   = time.LoadLocation("Asia/Yekaterinburg")
	updatedAt = time.Date(2026, 9, 28, 3, 0, 0, 0, time.UTC)
	center    = domain.Coordinate{Longitude: 56.2294, Latitude: 58.0105}
	source    = domain.Provenance{SourceName: "synthetic", FetchedAt: updatedAt}
)

func local(h, m int) time.Time { return time.Date(2026, 9, 28, h, m, 0, 0, perm).UTC() }

func rules(t *testing.T, raw string) domain.OpeningRules {
	t.Helper()
	r, err := domain.ParseOpeningRules([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

const everyDay = `{"schema_version":1,"weekly":{"mon":[["10:00","18:00"]],"tue":[["10:00","18:00"]],"wed":[["10:00","18:00"]],
"thu":[["10:00","18:00"]],"fri":[["10:00","18:00"]],"sat":[["10:00","18:00"]],"sun":[["10:00","18:00"]]},"closed_dates":[],"source_text":""}`

func place(t *testing.T, id byte, mode domain.DataMode, openingRules string) Place {
	category := domain.CategoryCulture
	p := Place{
		Place: domain.Place{
			ID: domain.PlaceID{id}, City: "perm", Title: "Place", Category: &category, Location: center,
			DataMode: mode, Provenance: source,
		},
		BaseScore: 1,
		Entrances: []domain.Entrance{{ID: domain.EntranceID{id}, PlaceID: domain.PlaceID{id}, Location: center}},
	}
	if openingRules != "" {
		p.Rules = rules(t, openingRules)
	}
	return p
}

func session(p Place, id byte, start, end time.Time, availability domain.Availability) domain.Candidate {
	window := domain.VisitWindow{Kind: domain.WindowFixed, Start: start, End: end, MinDuration: end.Sub(start), RecommendedDuration: end.Sub(start)}
	event := &domain.Event{ID: domain.EventID{id}, PlaceID: p.Place.ID, Title: "Event", Category: *p.Place.Category, DataMode: p.Place.DataMode, Provenance: source}
	return domain.Candidate{
		Place: p.Place, Event: event, Window: window, Entrances: p.Entrances, BaseScore: p.BaseScore,
		Session: &domain.Session{ID: domain.SessionID{id}, EventID: event.ID, Window: window, Access: domain.AccessFree,
			Availability: availability, Version: 1, DataMode: p.Place.DataMode, Provenance: source},
	}
}

func request() domain.OptimizeRequest {
	return domain.OptimizeRequest{
		City: "perm", Timezone: "Asia/Yekaterinburg", Start: local(10, 0), End: local(18, 0), Origin: center,
		Constraints: domain.RouteConstraints{MovementModes: []domain.MovementMode{domain.MovementWalk}, LoadProfile: "moderate", Budget: domain.Budget{Mode: domain.BudgetNone}},
	}
}

func testSlice(t *testing.T) *Slice {
	museum := place(t, 1, domain.DataSynthetic, everyDay)
	unknownHours := place(t, 2, domain.DataSynthetic, "")
	theatre := place(t, 3, domain.DataSynthetic, "")
	return &Slice{
		City: "perm", Timezone: "Asia/Yekaterinburg", Revision: 7, UpdatedAt: updatedAt,
		BuiltAt: local(9, 0), Horizon: local(9, 0).Add(-24 * time.Hour),
		Places: []Place{museum, unknownHours, theatre},
		Sessions: []domain.Candidate{
			session(theatre, 10, local(9, 0), local(10, 10), domain.AvailabilityAvailable), // only ten minutes of it in the day
			session(theatre, 11, local(12, 0), local(13, 0), domain.AvailabilityCancelled), // kept for obligation diagnostics
			session(theatre, 12, local(19, 0), local(20, 0), domain.AvailabilityAvailable), // after the day
			session(theatre, 13, local(14, 0), local(15, 0), domain.AvailabilitySoldOut),
		},
	}
}

func ids(cs []domain.Candidate) []byte {
	var out []byte
	for _, c := range cs {
		if c.Session != nil {
			out = append(out, c.Session.ID[0])
		} else {
			out = append(out, c.Place.ID[0])
		}
	}
	return out
}

func TestCandidatesExpandPlacesAndSessionsOfTheDay(t *testing.T) {
	cs, fresh, err := testSlice(t).Candidates(request(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// The museum's window, then sessions overlapping the day by their minimum duration, by start.
	if got := ids(cs); !slices.Equal(got, []byte{1, 11, 13}) {
		t.Fatalf("candidates %v", got)
	}
	if cs[0].Window.Start != local(10, 0) || cs[0].Window.End != local(18, 0) || cs[0].Window.MinDuration != domain.DefaultPlaceMinDuration {
		t.Fatalf("museum window %+v", cs[0].Window)
	}
	if fresh.CatalogRevision != 7 || fresh.DataAsOf == nil || !fresh.DataAsOf.Equal(updatedAt) || fresh.DataMode != domain.DataSynthetic {
		t.Fatalf("freshness %+v", fresh)
	}
}

func TestCandidatesKeepObligationsOutsideTheDay(t *testing.T) {
	req := request()
	late := domain.SessionID{12}
	short := domain.SessionID{10}
	req.Constraints.Obligations = []domain.Obligation{
		{SessionID: &late, Participation: domain.ParticipationUserReported},
		{SessionID: &short, Participation: domain.ParticipationUserReported},
	}
	cs, _, err := testSlice(t).Candidates(req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(cs); !slices.Equal(got, []byte{1, 10, 11, 13, 12}) {
		t.Fatalf("candidates %v", got)
	}
}

func TestCandidatesMergeExtraSessionsByStart(t *testing.T) {
	s := testSlice(t)
	old := session(s.Places[2], 20, local(11, 0), local(11, 30), domain.AvailabilityAvailable)
	req := request()
	id := old.Session.ID
	req.Constraints.Obligations = []domain.Obligation{{SessionID: &id, Participation: domain.ParticipationUserReported}}
	cs, _, err := s.Candidates(req, []domain.Candidate{old})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(cs); !slices.Equal(got, []byte{1, 20, 11, 13}) {
		t.Fatalf("candidates %v", got)
	}
}

func TestCandidatesRefuseAnotherTimezone(t *testing.T) {
	req := request()
	req.Timezone = "Europe/Moscow"
	if _, _, err := testSlice(t).Candidates(req, nil); !errors.Is(err, usecase.ErrInvalidRequest) {
		t.Fatalf("got %v", err)
	}
}

func TestFreshnessFallsBackToPlacesWithoutCandidates(t *testing.T) {
	s := &Slice{City: "perm", Timezone: "Asia/Yekaterinburg", Revision: 3, UpdatedAt: updatedAt,
		Places: []Place{place(t, 1, domain.DataPrepared, ""), place(t, 2, domain.DataSynthetic, "")}}
	cs, fresh, err := s.Candidates(request(), nil)
	if err != nil || len(cs) != 0 {
		t.Fatalf("%d candidates, %v", len(cs), err)
	}
	if fresh.DataMode != domain.DataSynthetic {
		t.Fatalf("mode %s", fresh.DataMode)
	}
	empty := &Slice{City: "perm", Timezone: "Asia/Yekaterinburg", Revision: 3, UpdatedAt: updatedAt}
	if _, fresh, _ := empty.Candidates(request(), nil); fresh.DataMode != domain.DataPrepared || fresh.Validate() != nil {
		t.Fatalf("empty catalog freshness %+v", fresh)
	}
}

func TestMissingAndCovers(t *testing.T) {
	s := testSlice(t)
	req := request()
	held, gone := domain.SessionID{11}, domain.SessionID{99}
	req.Constraints.Obligations = []domain.Obligation{
		{SessionID: &held, Participation: domain.ParticipationUserReported},
		{SessionID: &gone, Participation: domain.ParticipationUserReported},
		{SessionID: &gone, Participation: domain.ParticipationUserReported},
	}
	if got := s.Missing(req); len(got) != 1 || got[0] != gone {
		t.Fatalf("missing %v", got)
	}
	if !s.Covers(req) {
		t.Fatal("a day after the horizon is not covered")
	}
	req.Start = s.Horizon.Add(-time.Minute)
	if s.Covers(req) {
		t.Fatal("a day before the horizon is covered")
	}
}

func TestSizeGrowsWithTheCatalog(t *testing.T) {
	small, big := testSlice(t), testSlice(t)
	for i := range 50 {
		big.Sessions = append(big.Sessions, session(big.Places[2], byte(100+i), local(12, 0), local(13, 0), domain.AvailabilityAvailable))
	}
	if (&Slice{}).Size() <= 0 || big.Size() <= small.Size() {
		t.Fatalf("sizes %d and %d", small.Size(), big.Size())
	}
}
