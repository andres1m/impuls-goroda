package embedding

import (
	"bytes"
	"testing"

	"github.com/google/uuid"
)

func TestText(t *testing.T) {
	cases := map[string]struct {
		e    Entity
		want string
	}{
		"full": {
			Entity{
				Title:     "Эрмитаж",
				Category:  "Культура",
				Interests: []string{"Классические музеи и история", "Архитектура"},
			},
			"Эрмитаж. Культура. Интересы: Классические музеи и история, Архитектура",
		},
		"no interests": {Entity{Title: "Парк", Category: "Прогулки"}, "Парк. Прогулки"},
		"no category": {
			Entity{Title: "Площадь", Interests: []string{"Архитектура"}},
			"Площадь. Интересы: Архитектура",
		},
		"trimmed title": {Entity{Title: "  Сад  ", Category: "Прогулки"}, "Сад. Прогулки"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Text(&tc.e); got != tc.want {
				t.Fatalf("Text = %q", got)
			}
		})
	}
}

func TestStaleComparesStoredHash(t *testing.T) {
	id := uuid.New()
	fresh := Entity{Place: &id, Title: "Парк", Category: "Прогулки"}
	fresh.StoredHash = Hash(Text(&fresh))
	changed := fresh
	changed.Title = "Новый парк"
	missing := Entity{Event: &id, Title: "Концерт"}

	got := Stale([]Entity{fresh, changed, missing})
	if len(got) != 2 || got[0].Entity.Title != "Новый парк" || got[1].Entity.Title != "Концерт" {
		t.Fatalf("stale %+v", got)
	}
	if got[0].Text != "Новый парк. Прогулки" || !bytes.Equal(got[0].Hash, Hash(got[0].Text)) {
		t.Fatalf("prepared %+v", got[0])
	}
	if len(Hash("a")) != 32 || bytes.Equal(Hash("a"), Hash("b")) {
		t.Fatal("hash is not sha256 of the text")
	}
}
