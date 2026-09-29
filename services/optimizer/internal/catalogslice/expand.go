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
func (s *Slice) Candidates(
	req *domain.OptimizeRequest,
	extra []domain.Candidate,
) ([]domain.Candidate, domain.DataFreshness, error) {
	if s.Timezone != req.Timezone {
		return nil, domain.DataFreshness{}, fmt.Errorf(
			"%w: city %q timezone is %s, got %s",
			usecase.ErrInvalidRequest,
			s.City,
			s.Timezone,
			req.Timezone,
		)
	}
	loc, err := time.LoadLocation(req.Timezone)
	if err != nil {
		return nil, domain.DataFreshness{}, fmt.Errorf("%w: %w", usecase.ErrInvalidRequest, err)
	}
	start, end := req.Start.UTC(), req.End.UTC()

	candidates, err := s.expandPlaces(loc, start, end)
	if err != nil {
		return nil, domain.DataFreshness{}, err
	}

	obligations := obligated(req)
	allSessions := slices.Concat(s.Sessions, extra)
	var sessions []domain.Candidate
	for i := range allSessions {
		c := &allSessions[i]
		overlap := overlapDuration(c.Window.Start, c.Window.End, start, end)
		if _, ok := obligations[c.Session.ID]; ok || (overlap > 0 && overlap >= c.Window.MinDuration) {
			sessions = append(sessions, *c)
		}
	}
	slices.SortStableFunc(sessions, func(a, b domain.Candidate) int {
		return cmp.Or(a.Window.Start.Compare(b.Window.Start), bytes.Compare(a.Session.ID[:], b.Session.ID[:]))
	})
	candidates = append(candidates, sessions...)
	return candidates, s.freshness(candidates), nil
}

func (s *Slice) expandPlaces(loc *time.Location, start, end time.Time) ([]domain.Candidate, error) {
	var candidates []domain.Candidate
	for i := range s.Places {
		p := &s.Places[i]
		if p.Place.Category == nil {
			continue
		}
		if p.Rules.IsEmpty() {
			if c, ok := unknownHours(p, start, end); ok {
				candidates = append(candidates, c)
			}
			continue
		}
		windows, err := p.Rules.Windows(
			start,
			end,
			loc,
			domain.DefaultPlaceMinDuration,
			domain.DefaultPlaceRecommendedDuration,
		)
		if err != nil {
			return nil, fmt.Errorf("expand opening rules for place %x: %w", p.Place.ID, err)
		}
		for _, w := range windows {
			candidates = append(
				candidates,
				domain.Candidate{Place: p.Place, Window: w, Entrances: p.Entrances, BaseScore: p.BaseScore},
			)
		}
	}
	return candidates, nil
}

// unknownHours offers a place to eat that gave no opening hours as open all day; other places
// without hours are never visited on their own.
func unknownHours(p *Place, start, end time.Time) (domain.Candidate, bool) {
	if *p.Place.Category != domain.CategoryGastro || end.Sub(start) < domain.DefaultPlaceMinDuration {
		return domain.Candidate{}, false
	}
	return domain.Candidate{Place: p.Place, Entrances: p.Entrances, BaseScore: p.BaseScore, Window: domain.VisitWindow{
		Kind: domain.WindowContinuous, Start: start, End: end, MinDuration: domain.DefaultPlaceMinDuration,
		RecommendedDuration: domain.DefaultPlaceRecommendedDuration, HoursUnknown: true,
	}}, true
}

// freshness reports the weakest data mode the candidates rely on; with none, the places'.
func (s *Slice) freshness(candidates []domain.Candidate) domain.DataFreshness {
	mode := domain.DataPrepared
	switch {
	case len(candidates) > 0:
		mode = candidates[0].DataMode()
		for i := 1; i < len(candidates); i++ {
			mode = domain.Weakest(mode, candidates[i].DataMode())
		}
	case len(s.Places) > 0:
		mode = s.Places[0].Place.DataMode
		for i := 1; i < len(s.Places); i++ {
			mode = domain.Weakest(mode, s.Places[i].Place.DataMode)
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
