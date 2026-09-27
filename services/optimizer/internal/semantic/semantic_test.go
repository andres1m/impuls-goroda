package semantic

import (
	"context"
	"errors"
	"testing"

	"github.com/andres1m/impuls-goroda/pkg/ai"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

var space = ai.Space{Key: "openrouter/m", Version: "d3"}

type fakeEmbedder struct {
	err   error
	texts []string
}

func (f *fakeEmbedder) Space() ai.Space { return space }

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) (ai.Embedding, error) {
	f.texts = texts
	if f.err != nil {
		return ai.Embedding{}, f.err
	}
	return ai.Embedding{Space: space, Vectors: [][]float32{{1, 0, 0}}}, nil
}

type fakeStore struct {
	matches []Match
	err     error
	city    string
	space   ai.Space
	vector  []float32
	limit   int
}

func (f *fakeStore) NearestEntities(_ context.Context, city string, s ai.Space, vector []float32, limit int) ([]Match, error) {
	f.city, f.space, f.vector, f.limit = city, s, vector, limit
	return f.matches, f.err
}

func candidate(place, event, session byte) domain.Candidate {
	c := domain.Candidate{Place: domain.Place{ID: domain.PlaceID{place}}}
	if event != 0 {
		c.Event = &domain.Event{ID: domain.EventID{event}, PlaceID: c.Place.ID}
		c.Session = &domain.Session{ID: domain.SessionID{session}, EventID: c.Event.ID}
	}
	return c
}

func TestMatchesContains(t *testing.T) {
	m := NewMatches([]Match{
		{Place: &domain.PlaceID{1}},
		{Event: &domain.EventID{20}},
		{Session: &domain.SessionID{31}},
	})
	cases := map[string]struct {
		c    domain.Candidate
		want bool
	}{
		"place visit of a matched place":               {candidate(1, 0, 0), true},
		"session at a matched place":                   {candidate(1, 9, 9), true},
		"session of a matched event":                   {candidate(2, 20, 21), true},
		"matched session":                              {candidate(3, 30, 31), true},
		"other session of the matched session's event": {candidate(3, 30, 32), false},
		"unmatched place visit":                        {candidate(4, 0, 0), false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := m.Contains(tc.c); got != tc.want {
				t.Fatalf("Contains = %v", got)
			}
		})
	}
}

func TestRetrieverSearchesTheSpaceOfTheQueryVector(t *testing.T) {
	embedder := &fakeEmbedder{}
	store := &fakeStore{matches: []Match{{Place: &domain.PlaceID{1}}}}
	r, err := NewRetriever(embedder, store, 50)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Match(context.Background(), "perm", "тихие музеи")
	if err != nil {
		t.Fatal(err)
	}
	if len(embedder.texts) != 1 || embedder.texts[0] != "тихие музеи" {
		t.Fatalf("embedded %v", embedder.texts)
	}
	if store.city != "perm" || store.space != space || store.limit != 50 || len(store.vector) != 3 {
		t.Fatalf("store called with %s %v %v %d", store.city, store.space, store.vector, store.limit)
	}
	if !got.Contains(candidate(1, 0, 0)) {
		t.Fatal("match lost")
	}
}

func TestRetrieverWithoutVectors(t *testing.T) {
	r, _ := NewRetriever(&fakeEmbedder{}, &fakeStore{}, 10)
	if _, err := r.Match(context.Background(), "perm", "q"); !errors.Is(err, ErrNoVectors) {
		t.Fatalf("error %v", err)
	}
}

func TestRetrieverPassesFailures(t *testing.T) {
	r, _ := NewRetriever(&fakeEmbedder{err: ai.ErrNotConfigured}, &fakeStore{}, 10)
	if _, err := r.Match(context.Background(), "perm", "q"); !errors.Is(err, ai.ErrNotConfigured) {
		t.Fatalf("embed error %v", err)
	}
	storeErr := errors.New("db down")
	r, _ = NewRetriever(&fakeEmbedder{}, &fakeStore{err: storeErr}, 10)
	if _, err := r.Match(context.Background(), "perm", "q"); !errors.Is(err, storeErr) {
		t.Fatalf("store error %v", err)
	}
}

func TestNewRetrieverValidates(t *testing.T) {
	if _, err := NewRetriever(&fakeEmbedder{}, &fakeStore{}, 0); err == nil {
		t.Fatal("zero limit accepted")
	}
	if _, err := NewRetriever(nil, &fakeStore{}, 1); err == nil {
		t.Fatal("missing embedder accepted")
	}
	if _, err := NewRetriever(&fakeEmbedder{}, nil, 1); err == nil {
		t.Fatal("missing store accepted")
	}
}
