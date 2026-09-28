package normalize

import (
	"reflect"
	"strings"
	"testing"
)

type week map[string][][2]string

func days(open [][2]string, names ...string) week {
	w := week{"mon": {}, "tue": {}, "wed": {}, "thu": {}, "fri": {}, "sat": {}, "sun": {}}
	for _, n := range names {
		w[n] = open
	}
	return w
}

func span(from, to string) [2]string { return [2]string{from, to} }

var allDays = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

func TestParseOpeningHours(t *testing.T) {
	merge := func(ws ...week) week {
		out := days(nil)
		for _, w := range ws {
			for d, v := range w {
				if len(v) > 0 {
					out[d] = v
				}
			}
		}
		return out
	}
	for _, tc := range []struct {
		value string
		want  week
	}{
		{"24/7", days([][2]string{span("00:00", "24:00")}, allDays...)},
		{"Mo-Fr 10:00-20:00; Sa,Su 11:00-18:00", merge(
			days([][2]string{span("10:00", "20:00")}, "mon", "tue", "wed", "thu", "fri"),
			days([][2]string{span("11:00", "18:00")}, "sat", "sun"))},
		{"10:00-22:00", days([][2]string{span("10:00", "22:00")}, allDays...)},
		{"Fr-Mo 12:00-23:00", days([][2]string{span("12:00", "23:00")}, "fri", "sat", "sun", "mon")},
		{"Mo-Su 10:00-20:00; Mo off", days([][2]string{span("10:00", "20:00")}, "tue", "wed", "thu", "fri", "sat", "sun")},
		{"Mo-Su 00:00-24:00", days([][2]string{span("00:00", "24:00")}, allDays...)},
		{"Fr,Sa 18:00-02:00", merge(
			days([][2]string{span("18:00", "24:00")}, "fri"),
			days([][2]string{span("00:00", "02:00"), span("18:00", "24:00")}, "sat"),
			days([][2]string{span("00:00", "02:00")}, "sun"))},
		{"Su 20:00-03:00; Mo off", merge(
			days([][2]string{span("20:00", "24:00")}, "sun"),
			days([][2]string{span("00:00", "03:00")}, "mon"))},
		{"Mo-Fr 08:00-12:00,13:00-17:00", days([][2]string{span("08:00", "12:00"), span("13:00", "17:00")}, "mon", "tue", "wed", "thu", "fri")},
		{"Mo-Fr 10:00-20:00; Sa 10:00-12:00, 12:00-14:00", merge(
			days([][2]string{span("10:00", "20:00")}, "mon", "tue", "wed", "thu", "fri"),
			days([][2]string{span("10:00", "14:00")}, "sat"))},
		{"Mo-Su 09:00-21:00; Sa 10:00-18:00", merge(
			days([][2]string{span("09:00", "21:00")}, "mon", "tue", "wed", "thu", "fri", "sun"),
			days([][2]string{span("10:00", "18:00")}, "sat"))},
		{"Mo-Fr 09:00-18:00; ", days([][2]string{span("09:00", "18:00")}, "mon", "tue", "wed", "thu", "fri")},
		{"off", days(nil)},
		{"We-Su 10:00-18:00", days([][2]string{span("10:00", "18:00")}, "wed", "thu", "fri", "sat", "sun")},
	} {
		t.Run(tc.value, func(t *testing.T) {
			r, ok := ParseOpeningHours(tc.value)
			if !ok {
				t.Fatal("not parsed")
			}
			if !reflect.DeepEqual(week(r.Weekly), tc.want) {
				t.Fatalf("weekly\n got %v\nwant %v", r.Weekly, tc.want)
			}
			if r.SourceText != strings.TrimSpace(tc.value) {
				t.Fatalf("source text %q", r.SourceText)
			}
			if err := r.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestParseOpeningHoursRefusesWhatTheFormatCannotHold(t *testing.T) {
	for _, value := range []string{
		"", " ", "PH off", "Mo-Fr 10:00-20:00; PH off", "Jan-Mar 10:00-18:00", "sunrise-sunset", "10:00+",
		`Mo-Fr 10:00-20:00 "by appointment"`, "10:00:00-20:00:00", "Mo-Fr 25:00-26:00", `Mo-Fr 10:00-20:00 || "closed"`,
		"Mo-Fr", "Xx 10:00-20:00", "Mo-Fr 10:00-10:00", "Mo-Fr 24:00-02:00", "Mo-Fr 10-20", "Mo[1] 10:00-20:00",
		"24/7; Mo off", "Mo-Fr 10:00-20:00,", "Mo-Fr 9:00-18:00",
	} {
		t.Run(value, func(t *testing.T) {
			if r, ok := ParseOpeningHours(value); ok {
				t.Fatalf("parsed as %v", r.Weekly)
			}
		})
	}
}
