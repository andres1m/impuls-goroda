package main

import (
	"net/http"
	"testing"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

func TestParseIngestArgs(t *testing.T) {
	adapters := newAdapters(http.DefaultClient)

	adapter, city, err := parseIngestArgs([]string{"osm", "perm"}, adapters)
	if err != nil || adapter.Source().Key != domain.OSM || city != domain.Perm {
		t.Fatalf("osm perm: %v, %s, %v", adapter, city, err)
	}
	for _, args := range [][]string{{}, {"osm"}, {"unknown", "perm"}, {"osm", "spb"}, {"osm", "perm", "extra"}} {
		if _, _, err := parseIngestArgs(args, adapters); err == nil {
			t.Fatalf("args %q accepted", args)
		}
	}
}

func TestAllSourcesRegistered(t *testing.T) {
	adapters := newAdapters(http.DefaultClient)
	for _, key := range []domain.SourceKey{domain.MkrfEvents, domain.KudaGo, domain.OSM} {
		if a, ok := adapters[key]; !ok || a.Source().Key != key {
			t.Fatalf("source %s is not registered correctly", key)
		}
	}
}
