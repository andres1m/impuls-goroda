package enrich

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/andres1m/impuls-goroda/pkg/ai"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

type fakeModel struct {
	answer func(call int, p ai.Prompt) (string, error)
	calls  int
}

func (m *fakeModel) Complete(_ context.Context, p ai.Prompt) (string, error) {
	m.calls++
	return m.answer(m.calls, p)
}
func (m *fakeModel) Model() string { return "fake/model" }

type fakeStore struct {
	candidates []Candidate
	published  [][]Enriched
	changes    bool
}

func (s *fakeStore) Vocabulary(context.Context) ([]Tag, error) { return vocab, nil }
func (s *fakeStore) Candidates(context.Context, domain.City) ([]Candidate, error) {
	return s.candidates, nil
}
func (s *fakeStore) Publish(_ context.Context, _ domain.City, _ string, done []Enriched, _ time.Time) (bool, error) {
	s.published = append(s.published, done)
	return s.changes, nil
}

func place(title string, hasTags bool) Candidate {
	id := uuid.New()
	return Candidate{City: "perm", Place: &id, Title: title, Category: "Гастрономия", HasTags: hasTags}
}

func run(t *testing.T, s *fakeStore, m *fakeModel, batch int) Summary {
	t.Helper()
	got, err := Run(context.Background(), s, m, domain.Perm, batch, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestRunEnrichesInBatchesAndPublishesEachBatch(t *testing.T) {
	s := &fakeStore{candidates: []Candidate{place("a", false), place("b", false), place("c", true)}, changes: true}
	m := &fakeModel{answer: func(int, ai.Prompt) (string, error) {
		return `[{"n":1,"tags":["gastro_coffee"]},{"n":2,"tags":[]}]`, nil
	}}
	got := run(t, s, m, 2)
	if m.calls != 2 || got.Entities != 3 || got.Enriched != 3 || got.Published != 2 {
		t.Fatalf("calls %d summary %+v", m.calls, got)
	}
	if len(s.published) != 2 || len(s.published[0]) != 2 || len(s.published[1]) != 1 ||
		s.published[0][0].Mask != 1<<7 || s.published[0][1].Mask != 0 || s.published[1][0].Mask != 1<<7 {
		t.Fatalf("published %+v", s.published)
	}
}

func TestRunSkipsEntitiesWhoseHashMatches(t *testing.T) {
	c := place("a", true)
	c.StoredHash = Hash(Input(&c))
	c.StoredModel = "fake/model"
	s := &fakeStore{candidates: []Candidate{c}}
	m := &fakeModel{answer: func(int, ai.Prompt) (string, error) { t.Fatal("model called"); return "", nil }}
	if got := run(t, s, m, 10); got.Entities != 1 || got.Enriched != 0 || m.calls != 0 || len(s.published) != 0 {
		t.Fatalf("summary %+v calls %d", got, m.calls)
	}
}

func TestRunEnrichesAgainWhenTheModelChanged(t *testing.T) {
	c := place("a", true)
	c.StoredHash = Hash(Input(&c))
	c.StoredModel = "older/model"
	s := &fakeStore{candidates: []Candidate{c}, changes: true}
	m := &fakeModel{answer: func(int, ai.Prompt) (string, error) {
		return `[{"n":1,"tags":["gastro_coffee"]}]`, nil
	}}
	got := run(t, s, m, 10)
	if got.Enriched != 1 || m.calls != 1 || len(s.published) != 1 {
		t.Fatalf("summary %+v calls %d", got, m.calls)
	}
}

func TestRunFallsBackWhenTheModelFails(t *testing.T) {
	s := &fakeStore{candidates: []Candidate{place("tagged", true), place("bare", false)}}
	boom := errors.New("model down")
	m := &fakeModel{answer: func(int, ai.Prompt) (string, error) { return "", boom }}
	got := run(t, s, m, 10)
	if got.Fallback != 1 || got.Failed != 1 || got.Enriched != 0 || len(got.FailedTitles) != 1 ||
		got.FailedTitles[0] != "bare" || !errors.Is(got.LastError, boom) || len(s.published) != 0 {
		t.Fatalf("summary %+v", got)
	}
	if m.calls != 2 {
		t.Fatalf("attempts per batch: %d", m.calls)
	}
}

func TestRunRetriesAnUnusableAnswer(t *testing.T) {
	s := &fakeStore{candidates: []Candidate{place("a", false)}}
	m := &fakeModel{answer: func(call int, _ ai.Prompt) (string, error) {
		if call == 1 {
			return "no json here", nil
		}
		return `[{"n":1,"tags":["classical_art"]}]`, nil
	}}
	if got := run(t, s, m, 10); got.Enriched != 1 || m.calls != 2 {
		t.Fatalf("summary %+v calls %d", got, m.calls)
	}
}

func TestRunGivesUpAfterFiveFailedBatches(t *testing.T) {
	var cands []Candidate
	for i := range 10 {
		cands = append(cands, place(fmt.Sprint("p", i), false))
	}
	s := &fakeStore{candidates: cands}
	m := &fakeModel{answer: func(int, ai.Prompt) (string, error) { return "", errors.New("down") }}
	got := run(t, s, m, 1)
	if m.calls != 5*2 || got.Failed != 10 {
		t.Fatalf("calls %d summary %+v", m.calls, got)
	}
}

func TestRunLeavesItemsTheModelSkipped(t *testing.T) {
	s := &fakeStore{candidates: []Candidate{place("a", false), place("b", true)}}
	m := &fakeModel{answer: func(int, ai.Prompt) (string, error) { return `[{"n":1,"tags":["gastro_coffee"]}]`, nil }}
	got := run(t, s, m, 10)
	if got.Enriched != 1 || got.Fallback != 1 || got.Failed != 0 {
		t.Fatalf("summary %+v", got)
	}
}

func TestRunStopsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &fakeStore{candidates: []Candidate{place("a", false)}}
	m := &fakeModel{answer: func(int, ai.Prompt) (string, error) { return "", ctx.Err() }}
	if _, err := Run(ctx, s, m, domain.Perm, 10, time.Now); !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
}
