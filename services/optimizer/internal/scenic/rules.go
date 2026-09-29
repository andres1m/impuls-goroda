// Package scenic rates how pleasant the surroundings of a walk are, cell by cell of the H3 grid.
package scenic

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Rules pick the map areas that count as green or water. They are written in the area subset of
// osmium's tags-filter expressions, so one file drives both the map extract and the grid.
type Rules map[string]map[string]struct{}

// ParseRules reads lines such as "a/leisure=park,garden" or "a/water"; a key without values
// matches any value.
func ParseRules(r io.Reader) (Rules, error) {
	rules := Rules{}
	lines := bufio.NewScanner(r)
	for n := 1; lines.Scan(); n++ {
		if err := addRuleLine(rules, n, lines.Text()); err != nil {
			return nil, err
		}
	}
	if err := lines.Err(); err != nil {
		return nil, fmt.Errorf("scan rules: %w", err)
	}
	if len(rules) == 0 {
		return nil, errors.New("no rules")
	}
	return rules, nil
}

func addRuleLine(rules Rules, n int, raw string) error {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "#") {
		return nil
	}
	expr, ok := strings.CutPrefix(line, "a/")
	key, values, hasValues := strings.Cut(expr, "=")
	if !ok || key == "" || strings.ContainsAny(key, "!*,") ||
		(hasValues && (values == "" || strings.ContainsAny(values, "!*"))) {
		return fmt.Errorf("line %d: unsupported rule %q", n, line)
	}
	known, seen := rules[key]
	switch {
	case !hasValues:
		rules[key] = nil
	case seen && known == nil:
		// The key already matches any value.
	default:
		if known == nil {
			known = map[string]struct{}{}
			rules[key] = known
		}
		for v := range strings.SplitSeq(values, ",") {
			known[v] = struct{}{}
		}
	}
	return nil
}

func (r Rules) Match(tags map[string]string) bool {
	for key, values := range r {
		v, ok := tags[key]
		if !ok {
			continue
		}
		if values == nil {
			return true
		}
		if _, ok := values[v]; ok {
			return true
		}
	}
	return false
}
