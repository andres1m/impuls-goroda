package enrich

import (
	"errors"
	"strings"
	"testing"
)

var vocab = []Tag{
	{Bit: 0, Code: "contemporary_art", Title: "Современное искусство"},
	{Bit: 1, Code: "classical_art", Title: "Классические музеи и история"},
	{Bit: 7, Code: "gastro_coffee", Title: "Кофейни и локальная гастрономия"},
}

func TestBuildPromptCarriesOnlyTitlesCategoriesAndVocabulary(t *testing.T) {
	p := BuildPrompt(vocab, []Candidate{
		{Title: " Кофейня ", Category: "Гастрономия"},
		{Title: "Музей"},
	})
	if p.User != "1. Кофейня. Гастрономия\n2. Музей\n" {
		t.Fatalf("user prompt %q", p.User)
	}
	for _, tag := range vocab {
		if !strings.Contains(p.System, "- "+tag.Code+": "+tag.Title+"\n") {
			t.Fatalf("system prompt lacks %s: %q", tag.Code, p.System)
		}
	}
}

func TestHashIgnoresEverythingButTitleAndCategory(t *testing.T) {
	a := Candidate{Title: "Кофейня", Category: "Гастрономия", HasTags: true}
	b := Candidate{Title: "Кофейня", Category: "Гастрономия"}
	if string(Hash(Input(&a))) != string(Hash(Input(&b))) {
		t.Fatal("hash depends on more than title and category")
	}
	b.Title = "Кофейня 2"
	if string(Hash(Input(&a))) == string(Hash(Input(&b))) {
		t.Fatal("hash ignores the title")
	}
}

func TestParse(t *testing.T) {
	for _, c := range []struct {
		name, answer string
		want         map[int]int64
	}{
		{"plain", `[{"n":1,"tags":["gastro_coffee"]},{"n":2,"tags":["contemporary_art","classical_art"]}]`,
			map[int]int64{1: 1 << 7, 2: 1<<0 | 1<<1}},
		{"fenced", "```json\n[{\"n\":1,\"tags\":[\"classical_art\"]}]\n```", map[int]int64{1: 1 << 1}},
		{"unknown code dropped", `[{"n":1,"tags":["nope","gastro_coffee"]}]`, map[int]int64{1: 1 << 7}},
		{"only unknown codes is no answer", `[{"n":1,"tags":["coffee"]},{"n":2,"tags":["gastro_coffee"]}]`, map[int]int64{2: 1 << 7}},
		{"missing tags is no answer", `[{"n":1},{"n":2,"tags":null}]`, map[int]int64{}},
		{"empty tags are a valid answer", `[{"n":1,"tags":[]}]`, map[int]int64{1: 0}},
		{"number out of range ignored", `[{"n":0,"tags":["gastro_coffee"]},{"n":3,"tags":["gastro_coffee"]}]`, map[int]int64{}},
		{"duplicate number voids the item", `[{"n":1,"tags":["gastro_coffee"]},{"n":1,"tags":["classical_art"]}]`, map[int]int64{}},
	} {
		got, err := Parse(c.answer, 2, vocab)
		if err != nil || len(got) != len(c.want) {
			t.Fatalf("%s: %v %v", c.name, got, err)
		}
		for n, mask := range c.want {
			if m, ok := got[n]; !ok || m != mask {
				t.Fatalf("%s: item %d = %v, want %v (%v)", c.name, n, m, mask, got)
			}
		}
	}
}

func TestParseRejectsUnusableAnswers(t *testing.T) {
	for _, answer := range []string{"", "sorry, I cannot", `{"n":1}`, `[{"n":"one"}]`, "[1, 2"} {
		if _, err := Parse(answer, 2, vocab); !errors.Is(err, ErrAnswer) {
			t.Fatalf("%q: %v", answer, err)
		}
	}
}
