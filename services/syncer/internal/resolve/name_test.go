package resolve

import (
	"math"
	"testing"
)

func TestClean(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"ГБУК «Пермская художественная галерея»", "пермская художественная галерея"},
		{"Музей им. Прокофьева", "прокофьева"},
		{"Ланчи-бранчи", "ланчи бранчи"},
		{"Стадион", "стадион"},
		{"  ООО  «Кофе  Хаус» ", "кофе хаус"},
		{"", ""},
	} {
		if got := Clean(c.in); got != c.want {
			t.Errorf("Clean(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The reference values come from pg_trgm's similarity().
func TestTrigramMatchesPgTrgm(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want float64
	}{
		{"йоши тоши", "йоши тоши", 1},
		{"ланчи и бранчи", "ланчи-бранчи", 0.833333},
		{"пермский театр оперы и балета", "пермский театр оперы и балета чайковского", 0.707317},
		{"мхат", "с с прокофьева", 0},
		{"кофейня", "кофейня на набережной", 0.4},
		{"третьяковская галерея", "государственная третьяковская галерея", 0.611111},
		{"a1 b2", "a1  b2!", 1},
	} {
		if got := Trigram(c.a, c.b); math.Abs(got-c.want) > 1e-6 {
			t.Errorf("Trigram(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
	if Trigram("", "кофейня") != 0 || Trigram("", "") != 0 {
		t.Error("empty name is similar to nothing")
	}
}
