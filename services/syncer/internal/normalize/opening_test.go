package normalize

import (
	"encoding/json"
	"testing"
)

func validRules() OpeningRules {
	return OpeningRules{
		Weekly: map[string][][2]string{
			"mon": {{"10:00", "13:00"}, {"14:00", "18:00"}},
			"tue": {{"10:00", "18:00"}},
			"wed": {},
			"thu": {{"10:00", "18:00"}},
			"fri": {{"10:00", "18:00"}},
			"sat": {{"00:00", "24:00"}},
			"sun": {{"00:00", "24:00"}},
		},
		ClosedDates: []string{"2026-10-05"},
		SourceText:  "Mo-Fr 10:00-18:00",
	}
}

func TestOpeningRulesValidate(t *testing.T) {
	if err := validRules().Validate(); err != nil {
		t.Fatalf("valid rules rejected: %v", err)
	}
	cases := map[string]func(*OpeningRules){
		"missing day":       func(r *OpeningRules) { delete(r.Weekly, "wed") },
		"unknown day":       func(r *OpeningRules) { delete(r.Weekly, "wed"); r.Weekly["wen"] = nil },
		"midnight start":    func(r *OpeningRules) { r.Weekly["tue"] = [][2]string{{"24:00", "24:00"}} },
		"crosses midnight":  func(r *OpeningRules) { r.Weekly["tue"] = [][2]string{{"22:00", "02:00"}} },
		"overlap":           func(r *OpeningRules) { r.Weekly["tue"] = [][2]string{{"10:00", "14:00"}, {"13:00", "18:00"}} },
		"unsorted":          func(r *OpeningRules) { r.Weekly["tue"] = [][2]string{{"14:00", "18:00"}, {"10:00", "12:00"}} },
		"bad clock":         func(r *OpeningRules) { r.Weekly["tue"] = [][2]string{{"10:00", "25:00"}} },
		"single-digit hour": func(r *OpeningRules) { r.Weekly["tue"] = [][2]string{{"9:00", "18:00"}} },
		"bad closed date":   func(r *OpeningRules) { r.ClosedDates = []string{"05.10.2026"} },
	}
	for name, mutate := range cases {
		rules := validRules()
		mutate(&rules)
		if err := rules.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestOpeningRulesHasOpenHours(t *testing.T) {
	if !validRules().HasOpenHours() {
		t.Fatal("rules with intervals report no open hours")
	}
	closed := OpeningRules{
		Weekly: map[string][][2]string{"mon": {}, "tue": {}, "wed": {}, "thu": {}, "fri": {}, "sat": {}, "sun": {}},
	}
	if closed.HasOpenHours() {
		t.Fatal("rules without intervals report open hours")
	}
}

func TestOpeningRulesJSON(t *testing.T) {
	raw, err := validRules().MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		SchemaVersion int                    `json:"schema_version"`
		Weekly        map[string][][2]string `json:"weekly"`
		ClosedDates   []string               `json:"closed_dates"`
		SourceText    string                 `json:"source_text"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != 1 || len(got.Weekly) != 7 || len(got.Weekly["mon"]) != 2 {
		t.Fatalf("unexpected rules: %s", raw)
	}
	if wed, ok := got.Weekly["wed"]; !ok || wed == nil {
		t.Fatalf("a closed day must be an empty list, got %s", raw)
	}
	if got.ClosedDates[0] != "2026-10-05" || got.SourceText == "" {
		t.Fatalf("closed dates or source text lost: %s", raw)
	}
}
