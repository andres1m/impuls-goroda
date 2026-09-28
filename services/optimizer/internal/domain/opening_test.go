package domain

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestParseOpeningRulesEmpty(t *testing.T) {
	for _, raw := range [][]byte{nil, {}, []byte("  "), []byte("{}"), []byte(" { \n } ")} {
		rules, err := ParseOpeningRules(raw)
		if err != nil {
			t.Fatalf("ParseOpeningRules(%q): %v", raw, err)
		}
		if !rules.IsEmpty() {
			t.Fatalf("ParseOpeningRules(%q) expected empty rules", raw)
		}
		windows, err := rules.Windows(
			time.Date(2026, 9, 28, 5, 0, 0, 0, time.UTC),
			time.Date(2026, 9, 28, 17, 0, 0, 0, time.UTC),
			time.UTC,
			DefaultPlaceMinDuration,
			DefaultPlaceRecommendedDuration,
		)
		if err != nil || len(windows) != 0 {
			t.Fatalf("empty rules windows = %v, %v", windows, err)
		}
	}
}

func TestParseOpeningRulesValid(t *testing.T) {
	raw := []byte(`{
		"schema_version": 1,
		"weekly": {
			"mon": [],
			"tue": [["10:00", "13:00"], ["14:00", "19:00"]],
			"wed": [["10:00", "19:00"]],
			"thu": [["12:00", "20:00"]],
			"fri": [["10:00", "22:00"]],
			"sat": [["00:00", "24:00"]],
			"sun": [["11:00", "18:00"]]
		},
		"closed_dates": ["2026-09-29"],
		"source_text": "Вт 10-13, 14-19; Сб круглосуточно"
	}`)
	rules, err := ParseOpeningRules(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rules.IsEmpty() {
		t.Fatal("expected non-empty rules")
	}
	if len(rules.Weekly["tue"]) != 2 || rules.Weekly["tue"][0] != (OpeningHours{Start: "10:00", End: "13:00"}) {
		t.Fatalf("unexpected tuesday hours: %+v", rules.Weekly["tue"])
	}
	if !slices.Equal(rules.ClosedDates, []string{"2026-09-29"}) {
		t.Fatalf("unexpected closed dates: %v", rules.ClosedDates)
	}
}

func TestParseOpeningRulesInvalid(t *testing.T) {
	cases := map[string]string{
		"invalid json": `{not-json}`,
		"missing schema version": `{
			"weekly": {"mon":[],"tue":[],"wed":[],"thu":[],"fri":[],"sat":[],"sun":[]}
		}`,
		"unsupported schema version": `{
			"schema_version": 2,
			"weekly": {"mon":[],"tue":[],"wed":[],"thu":[],"fri":[],"sat":[],"sun":[]}
		}`,
		"unknown top level field": `{
			"schema_version": 1,
			"weekly": {"mon":[],"tue":[],"wed":[],"thu":[],"fri":[],"sat":[],"sun":[]},
			"timezone": "Asia/Yekaterinburg"
		}`,
		"missing weekday": `{
			"schema_version": 1,
			"weekly": {"mon":[],"tue":[],"wed":[],"thu":[],"fri":[],"sat":[]}
		}`,
		"unknown weekday": `{
			"schema_version": 1,
			"weekly": {"mon":[],"tue":[],"wed":[],"thu":[],"fri":[],"sat":[],"sun":[],"hol":[]}
		}`,
		"null weekday slice": `{
			"schema_version": 1,
			"weekly": {"mon":null,"tue":[],"wed":[],"thu":[],"fri":[],"sat":[],"sun":[]}
		}`,
		"interval wrong length": `{
			"schema_version": 1,
			"weekly": {"mon":[["10:00"]],"tue":[],"wed":[],"thu":[],"fri":[],"sat":[],"sun":[]}
		}`,
		"unpadded hour": `{
			"schema_version": 1,
			"weekly": {"mon":[["9:00","18:00"]],"tue":[],"wed":[],"thu":[],"fri":[],"sat":[],"sun":[]}
		}`,
		"start at 24:00": `{
			"schema_version": 1,
			"weekly": {"mon":[["24:00","24:00"]],"tue":[],"wed":[],"thu":[],"fri":[],"sat":[],"sun":[]}
		}`,
		"end past 24:00": `{
			"schema_version": 1,
			"weekly": {"mon":[["10:00","24:01"]],"tue":[],"wed":[],"thu":[],"fri":[],"sat":[],"sun":[]}
		}`,
		"inverted interval": `{
			"schema_version": 1,
			"weekly": {"mon":[["18:00","10:00"]],"tue":[],"wed":[],"thu":[],"fri":[],"sat":[],"sun":[]}
		}`,
		"zero length interval": `{
			"schema_version": 1,
			"weekly": {"mon":[["10:00","10:00"]],"tue":[],"wed":[],"thu":[],"fri":[],"sat":[],"sun":[]}
		}`,
		"overlapping intervals": `{
			"schema_version": 1,
			"weekly": {"mon":[["10:00","15:00"],["14:00","18:00"]],"tue":[],"wed":[],"thu":[],"fri":[],"sat":[],"sun":[]}
		}`,
		"unsorted intervals": `{
			"schema_version": 1,
			"weekly": {"mon":[["14:00","18:00"],["10:00","12:00"]],"tue":[],"wed":[],"thu":[],"fri":[],"sat":[],"sun":[]}
		}`,
		"invalid closed date": `{
			"schema_version": 1,
			"weekly": {"mon":[],"tue":[],"wed":[],"thu":[],"fri":[],"sat":[],"sun":[]},
			"closed_dates": ["28.09.2026"]
		}`,
	}
	for name, body := range cases {
		if _, err := ParseOpeningRules([]byte(body)); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

func TestOpeningRulesWindowsTimezoneAndClosedDates(t *testing.T) {
	permLoc, err := time.LoadLocation("Asia/Yekaterinburg")
	if err != nil {
		t.Fatal(err)
	}
	rules, err := ParseOpeningRules([]byte(`{
		"schema_version": 1,
		"weekly": {
			"mon": [["10:00", "13:00"], ["14:00", "19:00"]],
			"tue": [["10:00", "19:00"]],
			"wed": [["10:00", "19:00"]],
			"thu": [],
			"fri": [],
			"sat": [],
			"sun": []
		},
		"closed_dates": ["2026-09-29"]
	}`))
	if err != nil {
		t.Fatal(err)
	}

	// 2026-09-28 is Monday; 2026-09-29 is Tuesday (closed via closed_dates); 2026-09-30 is Wednesday.
	start := time.Date(2026, 9, 28, 12, 35, 0, 0, permLoc).UTC()
	end := time.Date(2026, 9, 30, 18, 0, 0, 0, permLoc).UTC()

	windows, err := rules.Windows(start, end, permLoc, DefaultPlaceMinDuration, DefaultPlaceRecommendedDuration)
	if err != nil {
		t.Fatalf("Windows: %v", err)
	}
	// Monday 10:00-13:00 has only 25m overlap with [12:35, ...], which is < 30m MinDuration, so it is dropped.
	// Monday 14:00-19:00 fits. Tuesday is in closed_dates. Wednesday 10:00-19:00 fits.
	if len(windows) != 2 {
		t.Fatalf("got %d windows, want 2: %+v", len(windows), windows)
	}
	want := []VisitWindow{
		{
			Kind:                WindowContinuous,
			Start:               time.Date(2026, 9, 28, 14, 0, 0, 0, permLoc).UTC(),
			End:                 time.Date(2026, 9, 28, 19, 0, 0, 0, permLoc).UTC(),
			MinDuration:         DefaultPlaceMinDuration,
			RecommendedDuration: DefaultPlaceRecommendedDuration,
		},
		{
			Kind:                WindowContinuous,
			Start:               time.Date(2026, 9, 30, 10, 0, 0, 0, permLoc).UTC(),
			End:                 time.Date(2026, 9, 30, 19, 0, 0, 0, permLoc).UTC(),
			MinDuration:         DefaultPlaceMinDuration,
			RecommendedDuration: DefaultPlaceRecommendedDuration,
		},
	}
	if !slices.Equal(windows, want) {
		t.Fatalf("windows = %+v, want %+v", windows, want)
	}
	for _, w := range windows {
		if err := w.Validate(); err != nil {
			t.Fatalf("window validate: %v", err)
		}
	}
}

func TestOpeningRulesWindowsMergesAcrossMidnight(t *testing.T) {
	moscowLoc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	// Overnight Friday 18:00-24:00 + Saturday 00:00-02:00 must merge into one continuous window.
	rules, err := ParseOpeningRules([]byte(`{
		"schema_version": 1,
		"weekly": {
			"mon": [],
			"tue": [],
			"wed": [],
			"thu": [],
			"fri": [["18:00", "24:00"]],
			"sat": [["00:00", "02:00"]],
			"sun": []
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}

	// 2026-09-25 is Friday, 2026-09-26 is Saturday.
	// Note that Fri 23:45 to Sat 00:30 has only 15m on Friday and 30m on Saturday,
	// which after midnight merging is a 45m overlap with the merged [Fri 18:00, Sat 02:00] window.
	start := time.Date(2026, 9, 25, 23, 45, 0, 0, moscowLoc).UTC()
	end := time.Date(2026, 9, 26, 0, 30, 0, 0, moscowLoc).UTC()

	windows, err := rules.Windows(start, end, moscowLoc, 45*time.Minute, time.Hour)
	if err != nil {
		t.Fatalf("Windows: %v", err)
	}
	if len(windows) != 1 {
		t.Fatalf("got %d windows, want 1 merged window: %+v", len(windows), windows)
	}
	wantStart := time.Date(2026, 9, 25, 18, 0, 0, 0, moscowLoc).UTC()
	wantEnd := time.Date(2026, 9, 26, 2, 0, 0, 0, moscowLoc).UTC()
	if !windows[0].Start.Equal(wantStart) || !windows[0].End.Equal(wantEnd) {
		t.Fatalf("merged window = [%s, %s], want [%s, %s]", windows[0].Start, windows[0].End, wantStart, wantEnd)
	}
}

func TestOpeningRulesWindowsValidation(t *testing.T) {
	rules, err := ParseOpeningRules([]byte(`{
		"schema_version": 1,
		"weekly": {"mon":[["10:00","18:00"]],"tue":[],"wed":[],"thu":[],"fri":[],"sat":[],"sun":[]}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 9, 28, 18, 0, 0, 0, time.UTC)

	if _, err := rules.Windows(t0, t1, nil, DefaultPlaceMinDuration, DefaultPlaceRecommendedDuration); err == nil {
		t.Error("expected error for nil location")
	}
	if _, err := rules.Windows(t1, t0, time.UTC, DefaultPlaceMinDuration, DefaultPlaceRecommendedDuration); err == nil {
		t.Error("expected error for end before start")
	}
	if _, err := rules.Windows(t0, t1, time.UTC, 0, DefaultPlaceRecommendedDuration); err == nil {
		t.Error("expected error for non-positive minDuration")
	}
	if _, err := rules.Windows(t0, t1, time.UTC, time.Hour, 30*time.Minute); err == nil {
		t.Error("expected error for recommendedDuration < minDuration")
	}
}

func TestOpeningRulesWriteAsStored(t *testing.T) {
	raw := `{"schema_version":1,"weekly":{"fri":[],"mon":[["10:00","18:00"]],"sat":[],"sun":[],"thu":[],"tue":[],"wed":[]},"closed_dates":["2026-10-05"],"source_text":"Mon 10-18"}`
	rules, err := ParseOpeningRules([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	written, err := json.Marshal(rules)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != raw {
		t.Fatalf("written %s", written)
	}
	again, err := ParseOpeningRules(written)
	if err != nil || !reflect.DeepEqual(again, rules) {
		t.Fatalf("read back %+v: %v", again, err)
	}
}
