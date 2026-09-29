package normalize

import (
	"fmt"
	"slices"
	"strings"
)

const (
	dayMon = iota
	dayTue
	dayWed
	dayThu
	dayFri
	daySat
	daySun
	daysPerWeek  = 7
	dayAbbrevLen = 2
)

var osmDays = map[string]int{
	"Mo": dayMon,
	"Tu": dayTue,
	"We": dayWed,
	"Th": dayThu,
	"Fr": dayFri,
	"Sa": daySat,
	"Su": daySun,
}

type interval struct{ start, end int }

// ParseOpeningHours reads the common subset of the OSM opening_hours syntax: 24/7, rules of weekdays
// with time spans or off, spans past midnight. Anything else — holidays, months, sunrise, comments,
// fallbacks — is refused rather than approximated, and ok is false.
func ParseOpeningHours(value string) (rules OpeningRules, ok bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return OpeningRules{}, false
	}
	var week [daysPerWeek][]interval
	if value == "24/7" {
		for d := range week {
			week[d] = []interval{{0, minutesPerDay}}
		}
		return build(&week, value), true
	}
	// Spans past midnight add to the next day whatever later rules say about that day.
	var spill [daysPerWeek][]interval
	for rule := range strings.SplitSeq(value, ";") {
		rule = strings.TrimSpace(rule)
		if rule == "" {
			continue
		}
		selected, spans, valid := parseRule(rule)
		if !valid {
			return OpeningRules{}, false
		}
		applyRuleToWeek(&week, &spill, selected, spans)
	}
	for d := range week {
		week[d] = merged(append(week[d], spill[d]...))
	}
	rules = build(&week, value)
	if rules.Validate() != nil {
		return OpeningRules{}, false
	}
	return rules, true
}

func applyRuleToWeek(week, spill *[daysPerWeek][]interval, selected []int, spans []interval) {
	for _, d := range selected {
		week[d] = nil
		nextDay := (d + 1) % daysPerWeek
		spill[nextDay] = nil
		for _, s := range spans {
			if s.end > s.start {
				week[d] = append(week[d], s)
				continue
			}
			week[d] = append(week[d], interval{s.start, minutesPerDay})
			if s.end > 0 {
				spill[nextDay] = append(spill[nextDay], interval{0, s.end})
			}
		}
	}
}

// parseRule returns the days a rule covers and its spans; an off rule has no spans.
func parseRule(rule string) (selected []int, spans []interval, ok bool) {
	selector, times, found := strings.Cut(rule, " ")
	if _, isDay := osmDays[selector[:min(dayAbbrevLen, len(selector))]]; !found && isDay {
		return nil, nil, false
	}
	if found && startsWithDay(selector) {
		if selected, ok = parseDays(selector); !ok {
			return nil, nil, false
		}
		times = strings.TrimSpace(times)
	} else {
		selected = []int{dayMon, dayTue, dayWed, dayThu, dayFri, daySat, daySun}
		times = rule
	}
	if times == "off" || times == "closed" {
		return selected, nil, true
	}
	for part := range strings.SplitSeq(times, ",") {
		s, valid := parseSpan(strings.TrimSpace(part))
		if !valid {
			return nil, nil, false
		}
		spans = append(spans, s)
	}
	return selected, spans, true
}

func startsWithDay(s string) bool {
	_, ok := osmDays[s[:min(dayAbbrevLen, len(s))]]
	return ok
}

func parseDays(selector string) ([]int, bool) {
	var out []int
	for part := range strings.SplitSeq(selector, ",") {
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
		for d := first; ; d = (d + 1) % daysPerWeek {
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

func build(week *[daysPerWeek][]interval, source string) OpeningRules {
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
	return fmt.Sprintf("%02d:%02d", minutes/minutesPerHour, minutes%minutesPerHour)
}
