package domain

import (
	"math"
	"testing"
	"time"
)

func i64(v int64) *int64 { return &v }

func TestPriceValidate(t *testing.T) {
	tests := []struct {
		name  string
		price Price
		ok    bool
	}{
		{"free", Price{Status: PriceFree, Currency: "RUB", LowerMinor: i64(0), UpperMinor: i64(0)}, true},
		{"free with amount", Price{Status: PriceFree, Currency: "RUB", LowerMinor: i64(0), UpperMinor: i64(1)}, false},
		{"fixed", Price{Status: PriceFixed, Currency: "RUB", LowerMinor: i64(5), UpperMinor: i64(5)}, true},
		{"fixed unequal", Price{Status: PriceFixed, Currency: "RUB", LowerMinor: i64(5), UpperMinor: i64(6)}, false},
		{"range", Price{Status: PriceRange, Currency: "RUB", LowerMinor: i64(3), UpperMinor: i64(7)}, true},
		{"range inverted", Price{Status: PriceRange, Currency: "RUB", LowerMinor: i64(7), UpperMinor: i64(3)}, false},
		{"negative", Price{Status: PriceFixed, Currency: "RUB", LowerMinor: i64(-1), UpperMinor: i64(-1)}, false},
		{"unknown", Price{Status: PriceUnknown, Currency: "RUB"}, true},
		{"unknown with bound", Price{Status: PriceUnknown, Currency: "RUB", UpperMinor: i64(1)}, false},
		{"missing currency", Price{Status: PriceUnknown}, false},
		{"lowercase currency", Price{Status: PriceUnknown, Currency: "rub"}, false},
		{"invalid status", Price{Status: "cheap", Currency: "RUB"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.price.Validate(); (err == nil) != tt.ok {
				t.Fatalf("Validate() = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

func TestPriceUpperBound(t *testing.T) {
	tests := []struct {
		name  string
		price Price
		want  int64
		known bool
	}{
		{"fixed", Price{Status: PriceFixed, Currency: "RUB", LowerMinor: i64(5), UpperMinor: i64(5)}, 5, true},
		{"range", Price{Status: PriceRange, Currency: "RUB", LowerMinor: i64(3), UpperMinor: i64(7)}, 7, true},
		{"free", Price{Status: PriceFree, Currency: "RUB", LowerMinor: i64(0), UpperMinor: i64(0)}, 0, true},
		{"unknown", Price{Status: PriceUnknown, Currency: "RUB"}, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, known := tt.price.UpperBound()
			if known != tt.known {
				t.Fatalf("known = %v, want %v", known, tt.known)
			}
			if known && (got.AmountMinor != tt.want || got.Currency != "RUB") {
				t.Fatalf("UpperBound() = %+v, want %d RUB", got, tt.want)
			}
		})
	}
}

func TestMoneyValidate(t *testing.T) {
	if err := (Money{AmountMinor: 0, Currency: "RUB"}).Validate(); err != nil {
		t.Fatalf("zero amount rejected: %v", err)
	}
	if err := (Money{AmountMinor: -1, Currency: "RUB"}).Validate(); err == nil {
		t.Fatal("negative amount accepted")
	}
	if err := (Money{AmountMinor: 1, Currency: "RU"}).Validate(); err == nil {
		t.Fatal("short currency accepted")
	}
}

func TestArchetypeMasks(t *testing.T) {
	tests := []struct {
		archetype Archetype
		want      InterestMask
	}{
		{ArchetypeUrbanAvantgarde, Interests(InterestContemporaryArt, InterestStreetWorkout, InterestGastroCoffee, InterestCinema)},
		{ArchetypeHistoryHeritage, Interests(InterestClassicalArt, InterestPerformingArts, InterestExcursions)},
		{ArchetypeActionSocial, Interests(InterestScienceTech, InterestRunningPark, InterestEcoVolunteer, InterestSocialVolunteer, InterestCityWalk, InterestLecturesWorkshops)},
	}
	for _, tt := range tests {
		if got := tt.archetype.Mask(); got != tt.want {
			t.Errorf("%s mask = %#x, want %#x", tt.archetype, got, tt.want)
		}
	}
	if Interests(0, 3, 7, 12) != ArchetypeUrbanAvantgarde.Mask() ||
		Interests(1, 8, 9) != ArchetypeHistoryHeritage.Mask() ||
		Interests(2, 4, 5, 6, 10, 11) != ArchetypeActionSocial.Mask() {
		t.Fatal("interest bit positions differ from the published taxonomy")
	}
	if err := Archetype("romantic").Validate(); err == nil {
		t.Fatal("unknown archetype accepted")
	}
}

func TestInterestMask(t *testing.T) {
	if got := InterestMask(0b1011).Matches(0b0110); got != 1 {
		t.Fatalf("Matches = %d, want 1", got)
	}
	if got := Interests(0, 12).Matches(Interests(0, 12, 5)); got != 2 {
		t.Fatalf("Matches = %d, want 2", got)
	}
	if !InterestMask(0).IsEmpty() || Interests(3).IsEmpty() {
		t.Fatal("IsEmpty is wrong")
	}
}

func TestCategoryIndex(t *testing.T) {
	seen := map[int]Category{}
	for _, c := range []Category{CategoryCulture, CategorySport, CategoryVolunteer, CategoryWalk, CategoryTourism, CategoryGastro} {
		if err := c.Validate(); err != nil {
			t.Fatalf("%s: %v", c, err)
		}
		i := c.Index()
		if i < 0 || i >= CategoryCount {
			t.Fatalf("%s index %d out of range", c, i)
		}
		if prev, dup := seen[i]; dup {
			t.Fatalf("%s and %s share index %d", c, prev, i)
		}
		seen[i] = c
	}
	if err := Category("park").Validate(); err == nil {
		t.Fatal("unknown category accepted")
	}
}

func TestWeakest(t *testing.T) {
	modes := []DataMode{DataSynthetic, DataPrepared, DataLive}
	for i, a := range modes {
		for j, b := range modes {
			want := modes[min(i, j)]
			if got := Weakest(a, b); got != want {
				t.Errorf("Weakest(%s, %s) = %s, want %s", a, b, got, want)
			}
		}
	}
}

func TestCoordinateValidate(t *testing.T) {
	bad := []Coordinate{
		{Longitude: math.NaN(), Latitude: 55},
		{Longitude: 37, Latitude: math.Inf(1)},
		{Longitude: 181, Latitude: 55},
		{Longitude: 37, Latitude: -91},
	}
	for _, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("%+v accepted", c)
		}
	}
	if err := (Coordinate{Longitude: 37.62, Latitude: 55.75}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestProvenanceValidate(t *testing.T) {
	now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	if err := (Provenance{SourceName: "kudago", FetchedAt: now}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Provenance{SourceName: "kudago"}).Validate(); err == nil {
		t.Fatal("missing fetch time accepted")
	}
	if err := (Provenance{FetchedAt: now}).Validate(); err == nil {
		t.Fatal("missing source accepted")
	}
	var zero SourceRecordID
	if err := (Provenance{SourceName: "kudago", FetchedAt: now, SourceRecordID: &zero}).Validate(); err == nil {
		t.Fatal("zero source record accepted")
	}
}

func TestRequireID(t *testing.T) {
	if err := requireID(PlaceID{}, "place"); err == nil {
		t.Fatal("zero id accepted")
	}
	if err := requireID(PlaceID{1}, "place"); err != nil {
		t.Fatal(err)
	}
}

func TestPriceValidateMissingBounds(t *testing.T) {
	bad := []Price{
		{Status: PriceFree, Currency: "RUB", UpperMinor: i64(0)},
		{Status: PriceFree, Currency: "RUB", LowerMinor: i64(0)},
		{Status: PriceFree, Currency: "RUB", LowerMinor: i64(1), UpperMinor: i64(0)},
		{Status: PriceFixed, Currency: "RUB", UpperMinor: i64(5)},
		{Status: PriceFixed, Currency: "RUB", LowerMinor: i64(5)},
		{Status: PriceRange, Currency: "RUB", UpperMinor: i64(5)},
		{Status: PriceRange, Currency: "RUB", LowerMinor: i64(5)},
		{Status: PriceRange, Currency: "RUB", LowerMinor: i64(-1), UpperMinor: i64(5)},
		{Status: PriceUnknown, Currency: "RUB", LowerMinor: i64(0)},
		{Status: PriceUnknown, Currency: "R1B"},
	}
	for _, p := range bad {
		if err := p.Validate(); err == nil {
			t.Errorf("%+v accepted", p)
		}
	}
}

func TestPriceUpperBoundOfMalformedPrice(t *testing.T) {
	if _, known := (Price{Status: PriceUnknown, Currency: "RUB", UpperMinor: i64(5)}).UpperBound(); known {
		t.Fatal("unknown price reported a bound")
	}
	if _, known := (Price{Status: PriceFixed, Currency: "RUB", LowerMinor: i64(5)}).UpperBound(); known {
		t.Fatal("price without an upper bound reported one")
	}
}

func TestCoordinateBounds(t *testing.T) {
	for _, c := range []Coordinate{{Longitude: -181}, {Latitude: 91}} {
		if err := c.Validate(); err == nil {
			t.Errorf("%+v accepted", c)
		}
	}
}

func TestSimpleValueValidation(t *testing.T) {
	if err := DataMode("guessed").Validate(); err == nil {
		t.Error("unknown data mode accepted")
	}
	if err := CatalogRevision(-1).Validate(); err == nil {
		t.Error("negative catalog revision accepted")
	}
	if err := CatalogRevision(0).Validate(); err != nil {
		t.Error(err)
	}
}

func TestMovementModeValidate(t *testing.T) {
	for _, mode := range []MovementMode{MovementWalk, MovementTransit, MovementCar} {
		if err := mode.Validate(); err != nil {
			t.Fatalf("%q rejected: %v", mode, err)
		}
	}
	for _, mode := range []MovementMode{"", " ", "bike", "WALK"} {
		if mode.Validate() == nil {
			t.Fatalf("%q accepted", mode)
		}
	}
}
