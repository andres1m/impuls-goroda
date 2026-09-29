package resolve

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/andres1m/impuls-goroda/pkg/ai"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

type fakeData struct {
	known    map[string]uuid.UUID
	existing map[uuid.UUID]bool
	nearby   []Candidate
}

func (d *fakeData) Known(context.Context, domain.City, domain.SourceKey, []string) (map[string]uuid.UUID, error) {
	return d.known, nil
}
func (d *fakeData) Existing(context.Context, domain.City, []uuid.UUID) (map[uuid.UUID]bool, error) {
	return d.existing, nil
}
func (d *fakeData) Nearby(context.Context, domain.City, domain.SourceKey, float64, float64) ([]Candidate, error) {
	return d.nearby, nil
}

type fakeEmbedder struct {
	calls   int
	vectors func(text string) []float32
	err     error
	// failAfter makes every call after that many succeed calls fail.
	failAfter int
}

func (e *fakeEmbedder) Embed(_ context.Context, texts []string) (ai.Embedding, error) {
	e.calls++
	if e.err != nil {
		return ai.Embedding{}, e.err
	}
	if e.failAfter > 0 && e.calls > e.failAfter {
		return ai.Embedding{}, errors.New("model down")
	}
	out := ai.Embedding{Space: ai.Space{Key: "fake", Version: "d2"}}
	for _, s := range texts {
		out.Vectors = append(out.Vectors, e.vectors(s))
	}
	return out, nil
}
func (e *fakeEmbedder) Space() ai.Space { return ai.Space{Key: "fake", Version: "d2"} }

func same(string) []float32 { return []float32{1, 0} }

func newPlace(source domain.SourceKey, key, title string) Place {
	return Place{Source: source, Key: key, OwnID: uuid.New(), Title: title,
		NormalizedTitle: Clean(title), Category: "gastro", Lat: 58.2, Lon: 56.4}
}

func cand(title string, distance float64) Candidate {
	return Candidate{PlaceID: uuid.New(), Title: title, NormalizedTitle: Clean(title), Category: "gastro", Distance: distance}
}

func resolveOne(t *testing.T, d *fakeData, e *fakeEmbedder, p Place) Resolution {
	t.Helper()
	r := &Resolver{Data: d}
	if e != nil {
		r.Embedder = e
	}
	got, err := r.Resolve(context.Background(), domain.Perm, []Place{p})
	if err != nil || len(got) != 1 {
		t.Fatalf("resolve: %v %v", got, err)
	}
	return got[0]
}

func TestMergesOneCandidateAboveTheThreshold(t *testing.T) {
	c := cand("Кофейня Центральная", 4)
	got := resolveOne(t, &fakeData{nearby: []Candidate{c}}, &fakeEmbedder{vectors: same},
		newPlace(domain.KudaGo, "p1", "ГБУК Кофейня Центральная"))
	if got.Kind != Merge || got.PlaceID != c.PlaceID || got.Score < Threshold || got.Known {
		t.Fatalf("resolution %+v", got)
	}
}

func TestKeepsSeparateWhenNothingReachesTheThreshold(t *testing.T) {
	e := &fakeEmbedder{vectors: same}
	got := resolveOne(t, &fakeData{nearby: []Candidate{cand("Музей С. С. Прокофьева", 45)}}, e,
		newPlace(domain.KudaGo, "p1", "Музей МХАТ"))
	if got.Kind != Own || e.calls != 0 {
		t.Fatalf("resolution %+v, embedder calls %d: an unreachable pair needs no vector", got, e.calls)
	}
}

func TestVectorsDecideBorderlineNames(t *testing.T) {
	// Same name, 4 m apart, but the model finds the two texts unrelated: 0.45 + 0.40·0 + 0.15·0.92 < 0.85.
	c := cand("Кофейня Центральная", 4)
	c.Category = "walk"
	byCategory := func(text string) []float32 {
		if strings.HasSuffix(text, "gastro") {
			return []float32{1, 0}
		}
		return []float32{0, 1}
	}
	e := &fakeEmbedder{vectors: byCategory}
	got := resolveOne(t, &fakeData{nearby: []Candidate{c}}, e, newPlace(domain.KudaGo, "p1", "Кофейня Центральная"))
	if got.Kind != Own || e.calls != 1 {
		t.Fatalf("resolution %+v, embedder calls %d", got, e.calls)
	}
}

func TestTwoCandidatesAboveTheThresholdAreAmbiguous(t *testing.T) {
	got := resolveOne(t, &fakeData{nearby: []Candidate{cand("Кофейня Центральная", 4), cand("Кофейня Центральная", 6)}},
		&fakeEmbedder{vectors: same}, newPlace(domain.KudaGo, "p1", "Кофейня Центральная"))
	if got.Kind != Review || got.Reason != ReasonAmbiguous {
		t.Fatalf("resolution %+v", got)
	}
}

func TestWithoutAVectorAReachablePairStaysSeparateForReview(t *testing.T) {
	near := &fakeData{nearby: []Candidate{cand("Кофейня Центральная", 4)}}
	p := newPlace(domain.KudaGo, "p1", "Кофейня Центральная")
	for name, e := range map[string]*fakeEmbedder{"model error": {err: errors.New("down")}, "no model": nil} {
		got := resolveOne(t, near, e, p)
		if got.Kind != Review || got.Reason != ReasonNoVector {
			t.Fatalf("%s: resolution %+v", name, got)
		}
	}
}

func TestStableDecisions(t *testing.T) {
	target := uuid.New()
	p := newPlace(domain.KudaGo, "p1", "Кофейня Центральная")
	e := &fakeEmbedder{err: errors.New("down")}

	got := resolveOne(t, &fakeData{known: map[string]uuid.UUID{"p1": target}, nearby: []Candidate{cand("Кофейня Центральная", 4)}}, e, p)
	if got.Kind != Merge || got.PlaceID != target || !got.Known || e.calls != 0 {
		t.Fatalf("a place key already merged is not scored again: %+v", got)
	}
	got = resolveOne(t, &fakeData{known: map[string]uuid.UUID{"p1": p.OwnID}}, e, p)
	if got.Kind != Own {
		t.Fatalf("a place key that resolved to its own place stays own: %+v", got)
	}
	got = resolveOne(t, &fakeData{existing: map[uuid.UUID]bool{p.OwnID: true}, nearby: []Candidate{cand("Кофейня Центральная", 4)}}, e, p)
	if got.Kind != Own || e.calls != 0 {
		t.Fatalf("an existing own place is not re-resolved: %+v", got)
	}
}

func TestSyntheticPlacesNeverMerge(t *testing.T) {
	e := &fakeEmbedder{vectors: same}
	got := resolveOne(t, &fakeData{nearby: []Candidate{cand("Кофейня Центральная", 4)}}, e,
		newPlace(domain.SyntheticSource, "p1", "Кофейня Центральная"))
	if got.Kind != Own || e.calls != 0 {
		t.Fatalf("resolution %+v", got)
	}
}

func TestPlacesOfOneBatchFromDifferentSourcesMerge(t *testing.T) {
	a := newPlace(domain.OSM, "n1", "Кофейня Центральная")
	b := newPlace(domain.KudaGo, "p1", "Кофейня Центральная")
	a2 := newPlace(domain.OSM, "n2", "Кофейня Центральная")
	got, err := (&Resolver{Data: &fakeData{}, Embedder: &fakeEmbedder{vectors: same}}).
		Resolve(context.Background(), domain.Perm, []Place{a, b, a2})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Kind != Own || got[1].Kind != Merge || got[1].PlaceID != a.OwnID {
		t.Fatalf("resolutions %+v", got)
	}
	if got[2].Kind == Merge && got[2].PlaceID == a.OwnID {
		t.Fatalf("same-source places of a batch are never merged: %+v", got[2])
	}
}

func TestSeveralRecordsOfOneVenueDoNotMakeItAmbiguous(t *testing.T) {
	// Two events of one venue share the place key, hence the place; a record of another source joins it.
	a1 := newPlace(domain.MkrfEvents, "venue", "Дом музыки")
	a2 := a1
	b := newPlace(domain.KudaGo, "p1", "Дом музыки")
	got, err := (&Resolver{Data: &fakeData{}, Embedder: &fakeEmbedder{vectors: same}}).
		Resolve(context.Background(), domain.Perm, []Place{a1, a2, b})
	if err != nil {
		t.Fatal(err)
	}
	if got[2].Kind != Merge || got[2].PlaceID != a1.OwnID {
		t.Fatalf("resolutions %+v", got)
	}
}

func TestAPlaceFoundTwiceIsOneCandidate(t *testing.T) {
	// The catalog already holds the place of the first record, so it comes back from the search and as a
	// mate of the batch at once.
	existing := newPlace(domain.OSM, "n1", "Кофейня Центральная")
	b := newPlace(domain.KudaGo, "p1", "Кофейня Центральная")
	d := &fakeData{
		existing: map[uuid.UUID]bool{existing.OwnID: true},
		nearby:   []Candidate{{PlaceID: existing.OwnID, Title: existing.Title, NormalizedTitle: existing.NormalizedTitle, Category: "gastro", Distance: 0}},
	}
	got, err := (&Resolver{Data: d, Embedder: &fakeEmbedder{vectors: same}}).
		Resolve(context.Background(), domain.Perm, []Place{existing, b})
	if err != nil {
		t.Fatal(err)
	}
	if got[1].Kind != Merge || got[1].PlaceID != existing.OwnID {
		t.Fatalf("resolutions %+v", got)
	}
}

func TestAVenueKeyIsDecidedOncePerBatch(t *testing.T) {
	a := newPlace(domain.OSM, "n1", "Кофейня Центральная")
	k1 := newPlace(domain.KudaGo, "p1", "Кофейня Центральная")
	k2 := k1
	e := &fakeEmbedder{vectors: same, failAfter: 1}
	got, err := (&Resolver{Data: &fakeData{}, Embedder: e}).Resolve(context.Background(), domain.Perm, []Place{a, k1, k2})
	if err != nil {
		t.Fatal(err)
	}
	if got[1] != got[2] || got[1].Kind != Merge || e.calls != 1 {
		t.Fatalf("resolutions %+v, embedder calls %d", got, e.calls)
	}
}
