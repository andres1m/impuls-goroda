package usecase

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/andres1m/impuls-goroda/pkg/ai"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/semantic"
)

const semanticWarning = "SEMANTIC_UNAVAILABLE"

type fakeMatcher struct {
	matches []semantic.Match
	err     error
	// block makes the matcher wait for its context, like a provider that does not answer.
	block bool
	calls int
	city  string
	query string
}

func (f *fakeMatcher) Match(ctx context.Context, city, query string) (semantic.Matches, error) {
	f.calls++
	f.city, f.query = city, query
	if f.block {
		<-ctx.Done()
		return semantic.Matches{}, ctx.Err()
	}
	if f.err != nil {
		return semantic.Matches{}, f.err
	}
	return semantic.NewMatches(f.matches), nil
}

func placeMatch(id byte) semantic.Match { return semantic.Match{Place: &domain.PlaceID{id}} }

// optimizeSemantic also reports how many points the router was asked to connect: origin and
// destination plus one per planned candidate, so it shows which pool the search used.
func optimizeSemantic(
	t *testing.T,
	matcher SemanticMatcher,
	query string,
	change func(*domain.OptimizeRequest),
) (result domain.OptimizeResult, routerPoints int) {
	t.Helper()
	var opts []Option
	if matcher != nil {
		opts = append(opts, WithSemantic(matcher, 50*time.Millisecond))
	}
	provider := estimated()
	p, err := NewPlanner(config(), fakeSource{candidates: city()}, provider, zap.NewNop(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	req := request()
	req.Constraints.SemanticQuery = query
	if change != nil {
		change(&req)
	}
	res, err := p.Optimize(context.Background(), &req)
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Validate(); err != nil {
		t.Fatalf("invalid result: %v", err)
	}
	return res, len(provider.points)
}

const wholePoolPoints = 5

func visitedPlaces(res *domain.OptimizeResult) map[domain.PlaceID]bool {
	out := make(map[domain.PlaceID]bool)
	for i := range res.Routes {
		route := &res.Routes[i]
		for j := range route.Steps {
			if route.Steps[j].Catalog != nil {
				out[route.Steps[j].Catalog.PlaceID] = true
			}
		}
	}
	return out
}

func TestSemanticQueryNarrowsCandidates(t *testing.T) {
	matcher := &fakeMatcher{matches: []semantic.Match{placeMatch(1)}}
	res, points := optimizeSemantic(t, matcher, "тихий музей", nil)
	if res.Status != domain.ResultReady || len(res.Routes) == 0 || points != 3 {
		t.Fatalf("status %s with %d routes over %d points", res.Status, len(res.Routes), points)
	}
	if got := visitedPlaces(&res); len(got) != 1 || !got[domain.PlaceID{1}] {
		t.Fatalf("visited %v", got)
	}
	if slices.Contains(warningCodes(res.Warnings), semanticWarning) {
		t.Fatalf("warnings %v", warningCodes(res.Warnings))
	}
	if matcher.city != "perm" || matcher.query != "тихий музей" {
		t.Fatalf("matcher called with %q %q", matcher.city, matcher.query)
	}
}

func TestSemanticQueryKeepsObligations(t *testing.T) {
	matcher := &fakeMatcher{matches: []semantic.Match{placeMatch(1)}}
	res, _ := optimizeSemantic(t, matcher, "тихий музей", func(r *domain.OptimizeRequest) {
		r.Constraints.Obligations = []domain.Obligation{
			{SessionID: &domain.SessionID{3}, Participation: domain.ParticipationUserReported},
		}
	})
	if res.Status != domain.ResultReady || len(res.Routes) == 0 {
		t.Fatalf("status %s", res.Status)
	}
	for _, route := range res.Routes {
		if !slices.ContainsFunc(route.Steps, func(s domain.Step) bool {
			return s.Catalog != nil && s.Catalog.SessionID != nil && *s.Catalog.SessionID == domain.SessionID{3}
		}) {
			t.Fatal("obligation outside the matches was dropped")
		}
	}
	if got := visitedPlaces(&res); got[domain.PlaceID{2}] {
		t.Fatalf("unmatched place visited: %v", got)
	}
}

func TestEmptySemanticQueryPlansByInterests(t *testing.T) {
	matcher := &fakeMatcher{matches: []semantic.Match{placeMatch(1)}}
	res, points := optimizeSemantic(t, matcher, "", nil)
	if matcher.calls != 0 {
		t.Fatal("matcher called without a query")
	}
	if points != wholePoolPoints || slices.Contains(warningCodes(res.Warnings), semanticWarning) {
		t.Fatalf("%d points, warnings %v", points, warningCodes(res.Warnings))
	}
}

func TestSemanticFailureFallsBackToInterests(t *testing.T) {
	cases := map[string]struct {
		matcher SemanticMatcher
		change  func(*domain.OptimizeRequest)
	}{
		"not configured in the service": {matcher: nil},
		"local model":                   {matcher: &fakeMatcher{err: ai.ErrNotImplemented}},
		"no api key":                    {matcher: &fakeMatcher{err: ai.ErrNotConfigured}},
		"no vectors for the model":      {matcher: &fakeMatcher{err: semantic.ErrNoVectors}},
		"provider failure":              {matcher: &fakeMatcher{err: errors.New("503")}},
		"slow provider":                 {matcher: &fakeMatcher{block: true}},
		"only excluded matches": {
			matcher: &fakeMatcher{matches: []semantic.Match{placeMatch(2)}},
			change: func(r *domain.OptimizeRequest) {
				r.Constraints.ExcludedCategories = []domain.Category{domain.CategoryWalk}
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res, points := optimizeSemantic(t, tc.matcher, "тихий музей", tc.change)
			if res.Status != domain.ResultReady || len(res.Routes) == 0 {
				t.Fatalf("status %s with %d routes", res.Status, len(res.Routes))
			}
			if !slices.Contains(warningCodes(res.Warnings), semanticWarning) {
				t.Fatalf("warnings %v", warningCodes(res.Warnings))
			}
			if want := wholePoolPoints; tc.change != nil {
				// The excluded walk is left out of the pool before the wishes are applied.
				if points != want-1 {
					t.Fatalf("fallback planned over %d points", points)
				}
			} else if points != want {
				t.Fatalf("fallback planned over %d points", points)
			}
		})
	}
}

func TestSemanticFallbackWarningAccompaniesNoFeasibleRoute(t *testing.T) {
	p, err := NewPlanner(
		config(),
		fakeSource{},
		estimated(),
		zap.NewNop(),
		WithSemantic(&fakeMatcher{err: ai.ErrNotConfigured}, time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	req := request()
	req.Constraints.SemanticQuery = "тихий музей"
	res, err := p.Optimize(context.Background(), &req)
	if err != nil {
		t.Fatal(err)
	}
	codes := warningCodes(res.Warnings)
	if res.Status != domain.ResultNoFeasibleRoute || !slices.Contains(codes, semanticWarning) ||
		!slices.Contains(codes, "NO_FEASIBLE_ROUTE") {
		t.Fatalf("status %s, warnings %v", res.Status, codes)
	}
}

func TestSemanticStopsWhenRequestIsCancelled(t *testing.T) {
	p, err := NewPlanner(
		config(),
		fakeSource{candidates: city()},
		estimated(),
		zap.NewNop(),
		WithSemantic(&fakeMatcher{block: true}, time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req := request()
	req.Constraints.SemanticQuery = "тихий музей"
	if _, err := p.Optimize(ctx, &req); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error %v", err)
	}
}
