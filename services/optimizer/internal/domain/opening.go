package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"
)

const (
	DefaultPlaceMinDuration         = 30 * time.Minute
	DefaultPlaceRecommendedDuration = time.Hour

	openingHoursPairLen = 2
	hoursPerDay         = 24
	minutesPerHour      = 60
	minutesPerDay       = hoursPerDay * minutesPerHour
	maxClockHour        = 23
	maxClockMinute      = 59
	clockFormatLen      = 5
	clockColonPos       = 2
	decimalBase         = 10
)

var weekdayKeys = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

type OpeningHours struct {
	Start string
	End   string
}

// MarshalJSON writes the interval as stored, ["10:00","18:00"].
func (h OpeningHours) MarshalJSON() ([]byte, error) {
	return json.Marshal([2]string{h.Start, h.End})
}

func (h *OpeningHours) UnmarshalJSON(data []byte) error {
	var raw []string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw) != openingHoursPairLen {
		return errors.New("opening hours interval must contain start and end")
	}
	h.Start, h.End = raw[0], raw[1]
	return nil
}

// OpeningRules is the weekly schedule of a place in the city's local time.
type OpeningRules struct {
	SchemaVersion int                       `json:"schema_version"`
	Weekly        map[string][]OpeningHours `json:"weekly"`
	ClosedDates   []string                  `json:"closed_dates"`
	SourceText    string                    `json:"source_text"`
}

func (r OpeningRules) IsEmpty() bool {
	return r.SchemaVersion == 0 && len(r.Weekly) == 0 && len(r.ClosedDates) == 0 && r.SourceText == ""
}

// ParseOpeningRules decodes the stored opening rules JSON; an empty object means hours are unknown.
func ParseOpeningRules(raw []byte) (OpeningRules, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("{}")) {
		return OpeningRules{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var r OpeningRules
	if err := dec.Decode(&r); err != nil {
		return OpeningRules{}, fmt.Errorf("decode opening rules: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return OpeningRules{}, errors.New("decode opening rules: unexpected trailing data")
	}
	if err := r.Validate(); err != nil {
		return OpeningRules{}, err
	}
	return r, nil
}

func (r OpeningRules) Validate() error {
	if r.IsEmpty() {
		return nil
	}
	if r.SchemaVersion != 1 {
		return fmt.Errorf("unsupported opening rules schema version %d", r.SchemaVersion)
	}
	if len(r.Weekly) != len(weekdayKeys) {
		return errors.New("opening rules must define all seven weekdays")
	}
	for _, day := range weekdayKeys {
		intervals, ok := r.Weekly[day]
		if !ok || intervals == nil {
			return fmt.Errorf("opening rules missing weekday %q", day)
		}
		if err := validateWeekdayIntervals(day, intervals); err != nil {
			return err
		}
	}
	return validateClosedDates(r.ClosedDates)
}

func validateWeekdayIntervals(day string, intervals []OpeningHours) error {
	prevEnd := -1
	for _, iv := range intervals {
		startMin, err := parseClockMinutes(iv.Start, false)
		if err != nil {
			return fmt.Errorf("%s start %q: %w", day, iv.Start, err)
		}
		endMin, err := parseClockMinutes(iv.End, true)
		if err != nil {
			return fmt.Errorf("%s end %q: %w", day, iv.End, err)
		}
		if endMin <= startMin {
			return fmt.Errorf("%s interval %s-%s must have end after start", day, iv.Start, iv.End)
		}
		if startMin < prevEnd {
			return fmt.Errorf("%s intervals must be sorted and non-overlapping", day)
		}
		prevEnd = endMin
	}
	return nil
}

func validateClosedDates(dates []string) error {
	for _, d := range dates {
		if len(d) != len(time.DateOnly) {
			return fmt.Errorf("invalid closed date %q", d)
		}
		if _, err := time.Parse(time.DateOnly, d); err != nil {
			return fmt.Errorf("invalid closed date %q: %w", d, err)
		}
	}
	return nil
}

type openingSpan struct {
	start time.Time
	end   time.Time
}

// Windows expands the weekly schedule into continuous UTC visit windows overlapping [start, end]
// by at least minDuration. Adjacent intervals across midnight are merged before filtering.
func (r OpeningRules) Windows(
	start, end time.Time,
	loc *time.Location,
	minDuration, recommendedDuration time.Duration,
) ([]VisitWindow, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	if loc == nil {
		return nil, errors.New("location is required")
	}
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return nil, errors.New("planning interval is invalid")
	}
	if minDuration <= 0 || recommendedDuration < minDuration {
		return nil, errors.New("visit window durations are invalid")
	}
	if r.IsEmpty() || !end.After(start) {
		return nil, nil
	}

	localStart := start.In(loc)
	localEnd := end.In(loc)
	firstDay := time.Date(localStart.Year(), localStart.Month(), localStart.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -1)
	lastDay := time.Date(localEnd.Year(), localEnd.Month(), localEnd.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)

	spans, err := r.buildSpans(firstDay, lastDay, loc)
	if err != nil {
		return nil, err
	}
	return filterOpeningWindows(spans, start.UTC(), end.UTC(), minDuration, recommendedDuration), nil
}

func (r OpeningRules) buildSpans(firstDay, lastDay time.Time, loc *time.Location) ([]openingSpan, error) {
	var spans []openingSpan
	for day := firstDay; !day.After(lastDay); day = day.AddDate(0, 0, 1) {
		if slices.Contains(r.ClosedDates, day.Format(time.DateOnly)) {
			continue
		}
		for _, iv := range r.Weekly[weekdayKey(day.Weekday())] {
			startMin, err := parseClockMinutes(iv.Start, false)
			if err != nil {
				return nil, err
			}
			endMin, err := parseClockMinutes(iv.End, true)
			if err != nil {
				return nil, err
			}
			spans = appendOrMergeSpan(
				spans,
				clockAt(day, startMin, loc).UTC(),
				clockAt(day, endMin, loc).UTC(),
			)
		}
	}
	return spans, nil
}

func appendOrMergeSpan(spans []openingSpan, s, e time.Time) []openingSpan {
	if n := len(spans); n > 0 && !s.After(spans[n-1].end) {
		if e.After(spans[n-1].end) {
			spans[n-1].end = e
		}
		return spans
	}
	return append(spans, openingSpan{start: s, end: e})
}

func filterOpeningWindows(
	spans []openingSpan,
	startUTC, endUTC time.Time,
	minDuration, recommendedDuration time.Duration,
) []VisitWindow {
	var windows []VisitWindow
	for _, sp := range spans {
		if sp.end.Sub(sp.start) < minDuration {
			continue
		}
		overlapStart := sp.start
		if startUTC.After(overlapStart) {
			overlapStart = startUTC
		}
		overlapEnd := sp.end
		if endUTC.Before(overlapEnd) {
			overlapEnd = endUTC
		}
		if overlapEnd.Sub(overlapStart) < minDuration {
			continue
		}
		windows = append(windows, VisitWindow{
			Kind:                WindowContinuous,
			Start:               sp.start,
			End:                 sp.end,
			MinDuration:         minDuration,
			RecommendedDuration: recommendedDuration,
		})
	}
	return windows
}

func weekdayKey(d time.Weekday) string {
	switch d {
	case time.Monday:
		return "mon"
	case time.Tuesday:
		return "tue"
	case time.Wednesday:
		return "wed"
	case time.Thursday:
		return "thu"
	case time.Friday:
		return "fri"
	case time.Saturday:
		return "sat"
	case time.Sunday:
		return "sun"
	default:
		return ""
	}
}

func clockAt(day time.Time, minutes int, loc *time.Location) time.Time {
	y, m, d := day.Date()
	if minutes == minutesPerDay {
		return time.Date(y, m, d+1, 0, 0, 0, 0, loc)
	}
	return time.Date(y, m, d, minutes/minutesPerHour, minutes%minutesPerHour, 0, 0, loc)
}

func parseClockMinutes(s string, allow2400 bool) (int, error) {
	if len(s) != clockFormatLen || s[clockColonPos] != ':' {
		return 0, errors.New("clock must be HH:MM")
	}
	h, okH := parseTwoDigits(s[0], s[1])
	m, okM := parseTwoDigits(s[3], s[4])
	if !okH || !okM {
		return 0, errors.New("clock must be HH:MM")
	}
	if allow2400 && h == hoursPerDay && m == 0 {
		return minutesPerDay, nil
	}
	if h > maxClockHour || m > maxClockMinute {
		return 0, errors.New("clock out of range")
	}
	return h*minutesPerHour + m, nil
}

func parseTwoDigits(a, b byte) (int, bool) {
	if a < '0' || a > '9' || b < '0' || b > '9' {
		return 0, false
	}
	return int(a-'0')*decimalBase + int(b-'0'), true
}
