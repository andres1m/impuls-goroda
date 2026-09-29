package seed

import (
	"testing"
	"time"
)

const testDataset = `
dataset_version: "test-1"
city: perm
places:
  - key: park
    kind: public_space
    title: "Парк  Тестовый "
    category: walk
    tags: [city_walk]
    lat: 58.0049119
    lon: 56.2476843
    opening_rules:
      weekly:
        mon: [["00:00", "24:00"]]
        tue: [["00:00", "24:00"]]
        wed: [["00:00", "24:00"]]
        thu: [["00:00", "24:00"]]
        fri: [["00:00", "24:00"]]
        sat: [["00:00", "24:00"]]
        sun: [["00:00", "24:00"]]
      source_text: круглосуточно
  - key: hall
    kind: venue
    title: Зал «Ёлка»
    lat: 58.0112132
    lon: 56.2390841
events:
  - key: talk
    place: hall
    title: Лекция
    category: culture
    tags: [lectures_workshops, science_tech]
    sessions:
      - key: evening
        days: [1]
        slot: fixed
        start: "17:00"
        end: "18:30"
        min_duration: 90m
        buffer: 15m
        late_entry: true
        last_entry: "17:20"
        access: ticket
        availability: available
        prices:
          - audience: general
            status: range
            amount: [300, 600]
            benefit_programs: [pushkin_card]
          - audience: child
            status: fixed
            amount: [150]
            eligibility_age: [7, 13]
  - key: shift
    place: hall
    title: Смена
    category: volunteer
    sessions:
      - key: morning
        slot: fixed
        start: "09:00"
        end: "12:00"
        min_duration: 3h
        access: registration
        registration_closes_before: 24h
        availability: registration_required
`

func testReference() Reference {
	return Reference{
		Categories: map[string]bool{
			"culture":   true,
			"sport":     true,
			"volunteer": true,
			"walk":      true,
			"tourism":   true,
			"gastro":    true,
		},
		TagBits: map[string]int{
			"contemporary_art": 0, "classical_art": 1, "science_tech": 2, "street_workout": 3, "running_park": 4,
			"eco_volunteer": 5, "social_volunteer": 6, "gastro_coffee": 7, "performing_arts": 8, "excursions": 9,
			"city_walk": 10, "lectures_workshops": 11, "cinema": 12,
		},
	}
}

func mustParse(t *testing.T) Dataset {
	t.Helper()
	ds, err := parse([]byte(testDataset))
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

func TestParseRejectsUnknownFields(t *testing.T) {
	if _, err := parse([]byte("dataset_version: \"1\"\ncity: perm\nplacez: []\n")); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestValidDatasetAccepted(t *testing.T) {
	ds := mustParse(t)
	if err := ds.validate(testReference()); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidDatasetRejected(t *testing.T) {
	cases := map[string]func(*Dataset){
		"empty version":           func(d *Dataset) { d.Version = "" },
		"bad key":                 func(d *Dataset) { d.Places[0].Key = "Park" },
		"duplicate place":         func(d *Dataset) { d.Places = append(d.Places, d.Places[0]) },
		"duplicate event":         func(d *Dataset) { d.Events = append(d.Events, d.Events[0]) },
		"empty title":             func(d *Dataset) { d.Places[1].Title = " " },
		"unknown kind":            func(d *Dataset) { d.Places[1].Kind = "museum" },
		"unknown place category":  func(d *Dataset) { d.Places[0].Category = "cinema" },
		"unknown event category":  func(d *Dataset) { d.Events[0].Category = "cinema" },
		"unknown tag":             func(d *Dataset) { d.Events[0].Tags = []string{"jazz"} },
		"latitude out of range":   func(d *Dataset) { d.Places[0].Lat = 91 },
		"public space no hours":   func(d *Dataset) { d.Places[0].OpeningRules = nil },
		"public space closed":     func(d *Dataset) { d.Places[0].OpeningRules.Weekly = closedWeek() },
		"invalid opening rules":   func(d *Dataset) { delete(d.Places[0].OpeningRules.Weekly, "mon") },
		"event at public space":   func(d *Dataset) { d.Events[0].Place = "park" },
		"unknown place":           func(d *Dataset) { d.Events[0].Place = "nowhere" },
		"venue unreachable":       func(d *Dataset) { d.Events = nil },
		"negative age":            func(d *Dataset) { age := -1; d.Events[0].AgeMin = &age },
		"no sessions":             func(d *Dataset) { d.Events[0].Sessions = nil },
		"duplicate session":       func(d *Dataset) { d.Events[0].Sessions = append(d.Events[0].Sessions, d.Events[0].Sessions[0]) },
		"unknown slot":            func(d *Dataset) { d.Events[0].Sessions[0].Slot = "daily" },
		"start after end":         func(d *Dataset) { d.Events[0].Sessions[0].Start = "19:00" },
		"min longer than session": func(d *Dataset) { d.Events[0].Sessions[0].MinDuration = 2 * time.Hour },
		"no min duration":         func(d *Dataset) { d.Events[0].Sessions[0].MinDuration = 0 },
		"recommended below min":   func(d *Dataset) { d.Events[0].Sessions[0].RecommendedDuration = 30 * time.Minute },
		"negative buffer":         func(d *Dataset) { d.Events[0].Sessions[0].Buffer = -time.Minute },
		"last entry no late":      func(d *Dataset) { d.Events[0].Sessions[0].LateEntry = nil },
		"last entry outside":      func(d *Dataset) { d.Events[0].Sessions[0].LastEntry = "19:00" },
		"day outside horizon":     func(d *Dataset) { d.Events[0].Sessions[0].Days = []int{7} },
		"duplicate day":           func(d *Dataset) { d.Events[0].Sessions[0].Days = []int{1, 1} },
		"unknown access":          func(d *Dataset) { d.Events[0].Sessions[0].Access = "paid" },
		"unknown availability":    func(d *Dataset) { d.Events[0].Sessions[0].Availability = "open" },
		"cancelled no reason":     func(d *Dataset) { d.Events[0].Sessions[0].Availability = "cancelled" },
		"reason not cancelled":    func(d *Dataset) { d.Events[0].Sessions[0].CancellationReason = "rain" },
		"deadline on ticket":      func(d *Dataset) { d.Events[0].Sessions[0].RegistrationClosesBefore = time.Hour },
		"ticket without prices":   func(d *Dataset) { d.Events[0].Sessions[0].Prices = nil },
		"prices on registration":  func(d *Dataset) { d.Events[1].Sessions[0].Prices = []Price{{Audience: "general", Status: "free"}} },
		"unknown audience":        func(d *Dataset) { d.Events[0].Sessions[0].Prices[0].Audience = "vip" },
		"duplicate audience":      func(d *Dataset) { d.Events[0].Sessions[0].Prices[1].Audience = "general" },
		"unknown status":          func(d *Dataset) { d.Events[0].Sessions[0].Prices[0].Status = "cheap" },
		"unknown with amount": func(d *Dataset) {
			d.Events[0].Sessions[0].Prices[0] = Price{Audience: "general", Status: "unknown", Amount: []int64{0}}
		},
		"fixed with two amounts": func(d *Dataset) { d.Events[0].Sessions[0].Prices[1].Amount = []int64{150, 200} },
		"inverted range":         func(d *Dataset) { d.Events[0].Sessions[0].Prices[0].Amount = []int64{600, 300} },
		"negative amount":        func(d *Dataset) { d.Events[0].Sessions[0].Prices[1].Amount = []int64{-1} },
		"bad eligibility":        func(d *Dataset) { d.Events[0].Sessions[0].Prices[1].EligibilityAge = []int{13, 7} },
		"unknown program":        func(d *Dataset) { d.Events[0].Sessions[0].Prices[0].BenefitPrograms = []string{"troika"} },
	}
	for name, mutate := range cases {
		ds := mustParse(t)
		mutate(&ds)
		if err := ds.validate(testReference()); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func closedWeek() map[string][][2]string {
	return map[string][][2]string{"mon": {}, "tue": {}, "wed": {}, "thu": {}, "fri": {}, "sat": {}, "sun": {}}
}

func TestTagMask(t *testing.T) {
	mask, err := tagMask([]string{"science_tech", "lectures_workshops"}, testReference())
	if err != nil || mask != 1<<2|1<<11 {
		t.Fatalf("mask = %b, %v", mask, err)
	}
}
