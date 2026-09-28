package catalogslice

import (
	"bytes"
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/usecase"
)

// Candidates expands the slice for the request: every place window within its interval and every
// session that overlaps it by the session's minimum length. Sessions the request's obligations name
// stay whatever their time or state, so a conflict can be explained. extra holds such sessions read
// outside the slice.
func (s *Slice) Candidates(req domain.OptimizeRequest, extra []domain.Candidate) ([]domain.Candidate, domain.DataFreshness, error) {
	if s.Timezone != req.Timezone {
		return nil, domain.DataFreshness{}, fmt.Errorf("%w: city %q timezone is %s, got %s", usecase.ErrInvalidRequest, s.City, s.Timezone, req.Timezone)
	}
	loc, err := time.LoadLocation(req.Timezone)
	if err != nil {
		return nil, domain.DataFreshness{}, fmt.Errorf("%w: %v", usecase.ErrInvalidRequest, err)
	}
	start, end := req.Start.UTC(), req.End.UTC()

	var candidates []domain.Candidate
	for _, p := range s.Places {
		if p.Place.Category == nil || p.Rules.IsEmpty() {
			continue
		}
		windows, err := p.Rules.Windows(start, end, loc, domain.DefaultPlaceMinDuration, domain.DefaultPlaceRecommendedDuration)
		if err != nil {
			return nil, domain.DataFreshness{}, fmt.Errorf("expand opening rules for place %x: %w", p.Place.ID, err)
		}
		for _, w := range windows {
			candidates = append(candidates, domain.Candidate{Place: p.Place, Window: w, Entrances: p.Entrances, BaseScore: p.BaseScore})
		}
	}

	obligations := obligated(req)
	var sessions []domain.Candidate
	for _, c := range slices.Concat(s.Sessions, extra) {
		overlap := overlapDuration(c.Window.Start, c.Window.End, start, end)
		if _, ok := obligations[c.Session.ID]; ok || (overlap > 0 && overlap >= c.Window.MinDuration) {
			sessions = append(sessions, c)
		}
	}
	slices.SortStableFunc(sessions, func(a, b domain.Candidate) int {
		return cmp.Or(a.Window.Start.Compare(b.Window.Start), bytes.Compare(a.Session.ID[:], b.Session.ID[:]))
	})
	candidates = append(candidates, sessions...)
	return candidates, s.freshness(candidates), nil
}

// freshness reports the weakest data mode the candidates rely on; with none, the places'.
func (s *Slice) freshness(candidates []domain.Candidate) domain.DataFreshness {
	mode := domain.DataPrepared
	switch {
	case len(candidates) > 0:
		mode = candidates[0].DataMode()
		for _, c := range candidates[1:] {
			mode = domain.Weakest(mode, c.DataMode())
		}
	case len(s.Places) > 0:
		mode = s.Places[0].Place.DataMode
		for _, p := range s.Places[1:] {
			mode = domain.Weakest(mode, p.Place.DataMode)
		}
	}
	asOf := s.UpdatedAt
	return domain.DataFreshness{DataMode: mode, DataAsOf: &asOf, CatalogRevision: s.Revision}
}

func overlapDuration(aStart, aEnd, bStart, bEnd time.Time) time.Duration {
	start := later(aStart, bStart)
	end := aEnd
	if bEnd.Before(end) {
		end = bEnd
	}
	if !end.After(start) {
		return 0
	}
	return end.Sub(start)
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
