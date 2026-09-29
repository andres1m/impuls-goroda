package resolve

import "testing"

// A pair is the same venue when a person would say so. Cosines are set by hand: no embedding model is
// available here, so only the name and distance terms are exercised on real strings.
func TestLabeledPairsAgainstTheThreshold(t *testing.T) {
	for _, c := range []struct {
		name     string
		a, b     string
		distance float64
		cosine   float64
		same     bool
	}{
		{"case only", "Йоши Тоши", "Йоши тоши", 9, 0.99, true},
		{"hyphen and conjunction", "Ланчи и бранчи", "Ланчи-бранчи", 11, 0.98, true},
		{"legal form", "ГБУК «Пермская художественная галерея»", "Пермская художественная галерея", 5, 0.96, true},
		{"generic word", "Музей им. Прокофьева", "Прокофьева", 20, 0.9, true},
		{"neighbouring museums", "Музей МХАТ", "Музей С. С. Прокофьева", 45, 0.35, false},
		{"different cafes", "Coffee Like", "Кофе Хаус", 20, 0.5, false},
		{"different sushi bars", "Суши Wok", "Чайка Суши", 11, 0.6, false},
		{"different stages", "Театр юного зрителя", "Театр оперы и балета", 30, 0.7, false},
	} {
		trigram := Trigram(Clean(c.a), Clean(c.b))
		got := Reachable(trigram, c.distance) && Score(trigram, c.cosine, c.distance) >= Threshold
		if got != c.same {
			t.Errorf("%s: %q / %q at %.0f m: trigram %.3f, score %.3f, merged=%v, want %v",
				c.name, c.a, c.b, c.distance, trigram, Score(trigram, c.cosine, c.distance), got, c.same)
		}
	}
}
