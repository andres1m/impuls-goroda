package catalogcache

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/catalogslice"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func fullSlice(t *testing.T) *catalogslice.Slice {
	t.Helper()
	rules, err := domain.ParseOpeningRules([]byte(`{"schema_version":1,"weekly":{"mon":[["10:00","18:00"]],"tue":[],"wed":[],"thu":[],"fri":[],"sat":[],"sun":[]},"closed_dates":["2026-10-05"],"source_text":"Mon 10-18"}`))
	if err != nil {
		t.Fatal(err)
	}
	category := domain.CategoryCulture
	record := domain.SourceRecordID{9}
	url := "https://example.org/buy"
	late := true
	amount := int64(50000)
	place := domain.Place{ID: domain.PlaceID{1}, City: "perm", Title: "Museum", Category: &category, InterestMask: 5,
		Location: center, DataMode: domain.DataLive, Provenance: domain.Provenance{SourceName: "live", SourceRecordID: &record, FetchedAt: now}}
	entrances := []domain.Entrance{{ID: domain.EntranceID{2}, PlaceID: place.ID, Location: center, AllowedModes: []domain.MovementMode{domain.MovementWalk},
		Accessibility: "confirmed", Verification: domain.VerificationVerified}}
	window := domain.VisitWindow{Kind: domain.WindowFixed, Start: now, End: now.Add(time.Hour), MinDuration: time.Hour, RecommendedDuration: time.Hour, LateEntryAllowed: &late}
	return &catalogslice.Slice{
		City: "perm", Timezone: "Asia/Yekaterinburg", Revision: 12, UpdatedAt: now, BuiltAt: now, Horizon: now.Add(-24 * time.Hour),
		Places: []catalogslice.Place{{Place: place, Rules: rules, BaseScore: 2.5, Entrances: entrances}},
		Sessions: []domain.Candidate{{
			Place: place, Window: window, Entrances: entrances, BaseScore: 2.5,
			Event: &domain.Event{ID: domain.EventID{3}, PlaceID: place.ID, Title: "Concert", Category: category, DataMode: domain.DataLive},
			Session: &domain.Session{ID: domain.SessionID{4}, EventID: domain.EventID{3}, Window: window, Access: domain.AccessTicket,
				Availability: domain.AvailabilitySoldOut, Version: 3, DataMode: domain.DataLive, Provenance: domain.Provenance{SourceName: "live", SourceURL: &url, FetchedAt: now}},
			Offers: []domain.PriceOffer{{ID: domain.PriceOfferID{5}, SessionID: domain.SessionID{4},
				Price:    domain.Price{Status: domain.PriceFixed, Currency: "RUB", LowerMinor: &amount, UpperMinor: &amount},
				Audience: domain.AudienceGeneral, BenefitPrograms: []string{domain.ProgramPushkinCard}}},
		}},
	}
}

func TestSliceSurvivesTheSharedCache(t *testing.T) {
	want := fullSlice(t)
	data, err := encode(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("slice changed on the way:\n got %+v\nwant %+v", got, want)
	}
	if err := got.Places[0].Rules.Validate(); err != nil {
		t.Fatalf("rules with closed days no longer valid: %v", err)
	}
	if _, err := decode([]byte("not a slice")); err == nil {
		t.Fatal("garbage decoded")
	}
}

func TestKeyNamesFormatCityAndRevision(t *testing.T) {
	if k := key("perm", 12); k != "optimizer:slice:v1:perm:12" || !strings.HasPrefix(k, "optimizer:slice:v1:") {
		t.Fatalf("key %q", k)
	}
}
