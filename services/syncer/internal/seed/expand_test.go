package seed

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/normalize"
	"github.com/google/uuid"
	"github.com/uber/h3-go/v4"
)

// 20:30 UTC on 30 September is already 1 October in Perm.
var testNow = time.Date(2026, 9, 30, 20, 30, 0, 0, time.UTC)

func findSession(t *testing.T, rows *Rows, externalID string) SessionRow {
	t.Helper()
	for i := range rows.Sessions {
		if rows.Sessions[i].ExternalID == externalID {
			return rows.Sessions[i]
		}
	}
	t.Fatalf("session %s not expanded", externalID)
	return SessionRow{}
}

func findPlace(t *testing.T, rows *Rows, externalID string) PlaceRow {
	t.Helper()
	for i := range rows.Places {
		if rows.Places[i].ExternalID == externalID {
			return rows.Places[i]
		}
	}
	t.Fatalf("place %s not expanded", externalID)
	return PlaceRow{}
}

func pricesOf(rows *Rows, sessionID uuid.UUID) map[string]PriceRow {
	prices := make(map[string]PriceRow)
	for i := range rows.Prices {
		p := &rows.Prices[i]
		if p.SessionID == sessionID {
			prices[p.Audience] = *p
		}
	}
	return prices
}

func TestExpandUsesCityTimezone(t *testing.T) {
	perm := mustParse(t)
	rows, err := Expand(&perm, testReference(), "2026-10-01", testNow)
	if err != nil {
		t.Fatal(err)
	}
	talk := findSession(t, &rows, "session:perm:talk:evening:2026-10-02")
	if want := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC); !talk.StartsAt.Equal(want) {
		t.Fatalf("perm start = %s, want %s", talk.StartsAt.UTC(), want)
	}
	if want := time.Date(2026, 10, 2, 13, 30, 0, 0, time.UTC); !talk.EndsAt.Equal(want) {
		t.Fatalf("perm end = %s, want %s", talk.EndsAt.UTC(), want)
	}
	if want := time.Date(
		2026,
		10,
		2,
		12,
		20,
		0,
		0,
		time.UTC,
	); talk.LastEntryAt == nil ||
		!talk.LastEntryAt.Equal(want) {
		t.Fatalf("perm last entry = %v, want %s", talk.LastEntryAt, want)
	}

	moscow := mustParse(t)
	moscow.City = "moscow"
	rows, err = Expand(&moscow, testReference(), "2026-10-01", testNow)
	if err != nil {
		t.Fatal(err)
	}
	talk = findSession(t, &rows, "session:moscow:talk:evening:2026-10-02")
	if want := time.Date(2026, 10, 2, 14, 0, 0, 0, time.UTC); !talk.StartsAt.Equal(want) {
		t.Fatalf("moscow start = %s, want %s", talk.StartsAt.UTC(), want)
	}
}

func TestExpandDefaultsToCityToday(t *testing.T) {
	ds := mustParse(t)
	rows, err := Expand(&ds, testReference(), "", testNow)
	if err != nil {
		t.Fatal(err)
	}
	var shifts []string
	for i := range rows.Sessions {
		s := &rows.Sessions[i]
		if s.StartsAt.Hour() == 9 {
			shifts = append(shifts, s.ExternalID)
		}
	}
	want := []string{
		"session:perm:shift:morning:2026-10-01", "session:perm:shift:morning:2026-10-02",
		"session:perm:shift:morning:2026-10-03", "session:perm:shift:morning:2026-10-04",
		"session:perm:shift:morning:2026-10-05", "session:perm:shift:morning:2026-10-06",
		"session:perm:shift:morning:2026-10-07",
	}
	if !reflect.DeepEqual(shifts, want) {
		t.Fatalf("shift sessions = %v, want %v", shifts, want)
	}
}

func TestExpandIsDeterministic(t *testing.T) {
	ds1 := mustParse(t)
	first, err := Expand(&ds1, testReference(), "2026-10-01", testNow)
	if err != nil {
		t.Fatal(err)
	}
	ds2 := mustParse(t)
	second, err := Expand(&ds2, testReference(), "2026-10-01", testNow.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Places, second.Places) || !reflect.DeepEqual(first.Events, second.Events) ||
		!reflect.DeepEqual(first.Prices, second.Prices) || len(first.Sessions) != len(second.Sessions) {
		t.Fatal("the same dataset and start date expanded differently")
	}
	for i := range first.Sessions {
		a, b := &first.Sessions[i], &second.Sessions[i]
		if a.ID != b.ID || !a.StartsAt.Equal(b.StartsAt) || !a.EndsAt.Equal(b.EndsAt) {
			t.Fatalf("session %s expanded differently", a.ExternalID)
		}
	}
	talk := findSession(t, &first, "session:perm:talk:evening:2026-10-02")
	if want := normalize.EntityID(("session:perm:talk:evening:2026-10-02")); talk.ID != want {
		t.Fatalf("session id = %s, want %s", talk.ID, want)
	}
}

func TestExpandSessionsAndPrices(t *testing.T) {
	ds := mustParse(t)
	rows, err := Expand(&ds, testReference(), "2026-10-01", testNow)
	if err != nil {
		t.Fatal(err)
	}
	assertTalkSessionAndPrices(t, &rows)
	assertShiftSessionAndPrices(t, &rows)
}

func assertTalkSessionAndPrices(t *testing.T, rows *Rows) {
	t.Helper()
	talk := findSession(t, rows, "session:perm:talk:evening:2026-10-02")
	if talk.SlotType != "FIXED_SESSION" || !talk.IsHard || talk.MinDurationS != 5400 ||
		talk.RecommendedDurationS != 5400 || talk.BufferS != 900 || talk.RegistrationDeadline != nil {
		t.Fatalf("unexpected talk session: %+v", talk)
	}
	prices := pricesOf(rows, talk.ID)
	general, child := prices["general"], prices["child"]
	if general.Status != statusRange || *general.AmountMin != 30000 || *general.AmountMax != 60000 ||
		*general.Currency != "RUB" || !reflect.DeepEqual(general.BenefitPrograms, []string{"pushkin_card"}) {
		t.Fatalf("unexpected general price: %+v", general)
	}
	if child.Status != statusFixed || *child.AmountMin != 15000 || *child.EligibilityAgeMin != 7 ||
		*child.EligibilityAgeMax != 13 {
		t.Fatalf("unexpected child price: %+v", child)
	}
}

func assertShiftSessionAndPrices(t *testing.T, rows *Rows) {
	t.Helper()
	shift := findSession(t, rows, "session:perm:shift:morning:2026-10-03")
	if shift.RegistrationDeadline == nil || !shift.RegistrationDeadline.Equal(shift.StartsAt.Add(-24*time.Hour)) {
		t.Fatalf("registration deadline = %v", shift.RegistrationDeadline)
	}
	free := pricesOf(rows, shift.ID)
	if len(free) != 1 || free["general"].Status != statusFree || *free["general"].AmountMin != 0 ||
		*free["general"].Currency != "RUB" || free["general"].BenefitPrograms == nil {
		t.Fatalf("unexpected free price: %+v", free)
	}
}

func TestExpandPlacesAndEvents(t *testing.T) {
	ds := mustParse(t)
	rows, err := Expand(&ds, testReference(), "2026-10-01", testNow)
	if err != nil {
		t.Fatal(err)
	}
	park := findPlace(t, &rows, "place:perm:park")
	if park.NormalizedTitle != "парк тестовый" || park.TagMask != 1<<10 || *park.Category != "walk" {
		t.Fatalf("unexpected park: %+v", park)
	}
	var rules map[string]any
	if err := json.Unmarshal(park.OpeningRules, &rules); err != nil || rules["schema_version"] != float64(1) {
		t.Fatalf("park opening rules = %s, %v", park.OpeningRules, err)
	}
	for resolution, cell := range map[int]int64{8: park.H3Res8, 11: park.H3Res11} {
		if c := h3.Cell(cell); !c.IsValid() || c.Resolution() != resolution {
			t.Fatalf("h3 res %d cell %d is invalid", resolution, cell)
		}
	}

	hall := findPlace(t, &rows, "place:perm:hall")
	if hall.NormalizedTitle != "зал «елка»" || hall.Category != nil || string(hall.OpeningRules) != "{}" {
		t.Fatalf("unexpected hall: %+v", hall)
	}
	talk := rows.Events[0]
	if talk.ExternalID != "event:perm:talk" || talk.PlaceID != hall.ID || talk.TagMask != 1<<11|1<<2 {
		t.Fatalf("unexpected event: %+v", talk)
	}
}

func TestExpandRejects(t *testing.T) {
	ds := mustParse(t)
	if _, err := Expand(&ds, testReference(), "01.10.2026", testNow); err == nil {
		t.Fatal("malformed start date accepted")
	}
	unknownCity := mustParse(t)
	unknownCity.City = "spb"
	if _, err := Expand(&unknownCity, testReference(), "2026-10-01", testNow); err == nil {
		t.Fatal("unknown city accepted")
	}
	invalid := mustParse(t)
	invalid.Version = ""
	if _, err := Expand(&invalid, testReference(), "2026-10-01", testNow); err == nil {
		t.Fatal("invalid dataset expanded")
	}
}
