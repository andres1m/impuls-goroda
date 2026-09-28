package normalize

import (
	"fmt"
	"slices"
	"strings"
)

var osmDays = map[string]int{"Mo": 0, "Tu": 1, "We": 2, "Th": 3, "Fr": 4, "Sa": 5, "Su": 6}

type interval struct{ start, end int }

// ParseOpeningHours reads the common subset of the OSM opening_hours syntax: 24/7, rules of weekdays
// with time spans or off, spans past midnight. Anything else — holidays, months, sunrise, comments,
// fallbacks — is refused rather than approximated, and ok is false.
func ParseOpeningHours(value string) (rules OpeningRules, ok bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return OpeningRules{}, false
	}
	var week [7][]interval
	if value == "24/7" {
		for d := range week {
			week[d] = []interval{{0, 24 * 60}}
		}
		return build(week, value), true
	}
	// Spans past midnight add to the next day whatever later rules say about that day.
	var spill [7][]interval
	for _, rule := range strings.Split(value, ";") {
		rule = strings.TrimSpace(rule)
		if rule == "" {
			continue
		}
		selected, spans, ok := parseRule(rule)
		if !ok {
			return OpeningRules{}, false
		}
		for _, d := range selected {
			week[d] = nil
			spill[(d+1)%7] = nil
			for _, s := range spans {
				if s.end > s.start {
					week[d] = append(week[d], s)
					continue
				}
				week[d] = append(week[d], interval{s.start, 24 * 60})
				if s.end > 0 {
					spill[(d+1)%7] = append(spill[(d+1)%7], interval{0, s.end})
				}
			}
		}
	}
	for d := range week {
		week[d] = merged(append(week[d], spill[d]...))
	}
	rules = build(week, value)
	if rules.Validate() != nil {
		return OpeningRules{}, false
	}
	return rules, true
}

// parseRule returns the days a rule covers and its spans; an off rule has no spans.
func parseRule(rule string) (selected []int, spans []interval, ok bool) {
	selector, times, found := strings.Cut(rule, " ")
	if _, isDay := osmDays[selector[:min(2, len(selector))]]; !found && isDay {
		return nil, nil, false
	}
	if found && startsWithDay(selector) {
		if selected, ok = parseDays(selector); !ok {
			return nil, nil, false
		}
		times = strings.TrimSpace(times)
	} else {
		selected = []int{0, 1, 2, 3, 4, 5, 6}
		times = rule
	}
	if times == "off" || times == "closed" {
		return selected, nil, true
	}
	for _, part := range strings.Split(times, ",") {
		s, ok := parseSpan(strings.TrimSpace(part))
		if !ok {
			return nil, nil, false
		}
		spans = append(spans, s)
	}
	return selected, spans, true
}

func startsWithDay(s string) bool {
	_, ok := osmDays[s[:min(2, len(s))]]
	return ok
}

func parseDays(selector string) ([]int, bool) {
	var out []int
	for _, part := range strings.Split(selector, ",") {
		from, to, isRange := strings.Cut(part, "-")
		first, ok := osmDays[from]
		if !ok {
			return nil, false
		}
		last := first
		if isRange {
			if last, ok = osmDays[to]; !ok {
				return nil, false
			}
		}
		for d := first; ; d = (d + 1) % 7 {
			out = append(out, d)
			if d == last {
				break
			}
		}
	}
	return out, true
}

// parseSpan reads HH:MM-HH:MM; an end not after the start means the span runs past midnight.
func parseSpan(s string) (interval, bool) {
	from, to, found := strings.Cut(s, "-")
	if !found {
		return interval{}, false
	}
	start, err := ClockMinutes(from, false)
	if err != nil {
		return interval{}, false
	}
	end, err := ClockMinutes(to, true)
	if err != nil || end == start {
		return interval{}, false
	}
	return interval{start, end}, true
}

func merged(in []interval) []interval {
	slices.SortFunc(in, func(a, b interval) int { return a.start - b.start })
	var out []interval
	for _, iv := range in {
		if n := len(out); n > 0 && iv.start <= out[n-1].end {
			out[n-1].end = max(out[n-1].end, iv.end)
			continue
		}
		out = append(out, iv)
	}
	return out
}

func build(week [7][]interval, source string) OpeningRules {
	weekly := make(map[string][][2]string, len(Weekdays))
	for d, day := range Weekdays {
		weekly[day] = [][2]string{}
		for _, iv := range week[d] {
			weekly[day] = append(weekly[day], [2]string{clock(iv.start), clock(iv.end)})
		}
	}
	return OpeningRules{Weekly: weekly, SourceText: source}
}

func clock(minutes int) string {
	return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
}
