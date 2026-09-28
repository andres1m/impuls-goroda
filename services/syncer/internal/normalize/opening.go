package normalize

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Weekdays are the keys of a weekly schedule, Monday first.
var Weekdays = [...]string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

const openingRulesSchemaVersion = 1

// OpeningRules are a place's weekly hours in the city's local time. An empty day is a day off.
type OpeningRules struct {
	Weekly      map[string][][2]string `yaml:"weekly"`
	ClosedDates []string               `yaml:"closed_dates"`
	SourceText  string                 `yaml:"source_text"`
}

func (r OpeningRules) Validate() error {
	if len(r.Weekly) != len(Weekdays) {
		return errors.New("weekly must list exactly the days mon..sun")
	}
	for _, day := range Weekdays {
		intervals, ok := r.Weekly[day]
		if !ok {
			return fmt.Errorf("weekly: %s is missing", day)
		}
		previousEnd := 0
		for i, interval := range intervals {
			start, err := ClockMinutes(interval[0], false)
			if err != nil {
				return fmt.Errorf("weekly %s: %w", day, err)
			}
			end, err := ClockMinutes(interval[1], true)
			if err != nil {
				return fmt.Errorf("weekly %s: %w", day, err)
			}
			if start >= end {
				return fmt.Errorf("weekly %s: %s-%s is empty or crosses midnight", day, interval[0], interval[1])
			}
			if i > 0 && start < previousEnd {
				return fmt.Errorf("weekly %s: intervals overlap or are not sorted", day)
			}
			previousEnd = end
		}
	}
	for _, date := range r.ClosedDates {
		if _, err := time.Parse(time.DateOnly, date); err != nil {
			return fmt.Errorf("closed date %q is not YYYY-MM-DD", date)
		}
	}
	return nil
}

func (r OpeningRules) HasOpenHours() bool {
	for _, intervals := range r.Weekly {
		if len(intervals) > 0 {
			return true
		}
	}
	return false
}

// MarshalJSON writes the stored catalog form: every day present and the schema version set.
func (r OpeningRules) MarshalJSON() ([]byte, error) {
	weekly := make(map[string][][2]string, len(Weekdays))
	for _, day := range Weekdays {
		weekly[day] = append([][2]string{}, r.Weekly[day]...)
	}
	return json.Marshal(struct {
		SchemaVersion int                    `json:"schema_version"`
		Weekly        map[string][][2]string `json:"weekly"`
		ClosedDates   []string               `json:"closed_dates,omitempty"`
		SourceText    string                 `json:"source_text,omitempty"`
	}{openingRulesSchemaVersion, weekly, r.ClosedDates, r.SourceText})
}

// ClockMinutes parses HH:MM; 24:00 is accepted only where the end of the day is meant.
func ClockMinutes(value string, endOfDayAllowed bool) (int, error) {
	if value == "24:00" && endOfDayAllowed {
		return 24 * 60, nil
	}
	if len(value) != 5 {
		return 0, fmt.Errorf("time %q is not HH:MM", value)
	}
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return 0, fmt.Errorf("time %q is not HH:MM", value)
	}
	return parsed.Hour()*60 + parsed.Minute(), nil
}
