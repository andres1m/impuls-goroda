package main

import (
	"errors"
	"slices"
	"testing"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/enrich"
)

func TestParseEnrichArgs(t *testing.T) {
	for _, c := range []struct {
		args   []string
		cities []domain.City
		batch  int
	}{
		{nil, []domain.City{domain.Moscow, domain.Perm}, 20},
		{[]string{"--city", "perm"}, []domain.City{domain.Perm}, 20},
		{[]string{"--batch=5"}, []domain.City{domain.Moscow, domain.Perm}, 5},
	} {
		cities, batch, err := parseEnrichArgs(c.args)
		if err != nil || !slices.Equal(cities, c.cities) || batch != c.batch {
			t.Fatalf("args %q: %v %d %v", c.args, cities, batch, err)
		}
	}
	for _, args := range [][]string{{"--city", "kazan"}, {"--batch", "0"}, {"--batch", "101"}, {"perm"}, {"--city"}} {
		if _, _, err := parseEnrichArgs(args); err == nil {
			t.Fatalf("args %q accepted", args)
		}
	}
}

func TestReportEnrichmentFailsOnlyWhenEntitiesHaveNoTags(t *testing.T) {
	ok := enrich.Summary{Entities: 3, Enriched: 1, Fallback: 2, LastError: errors.New("down")}
	if err := reportEnrichment(domain.Perm, &ok); err != nil {
		t.Fatalf("fallback is not a failure: %v", err)
	}
	bad := enrich.Summary{Entities: 3, Failed: 1, FailedTitles: []string{"Кофейня"}}
	if err := reportEnrichment(domain.Perm, &bad); err == nil {
		t.Fatal("failed entities went unnoticed")
	}
}
