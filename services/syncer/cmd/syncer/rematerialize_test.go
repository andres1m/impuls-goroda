package main

import (
	"testing"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

func TestParseRematerializeArgs(t *testing.T) {
	source, city, err := parseRematerializeArgs([]string{"mkrf_events", "perm"})
	if err != nil || source != domain.MkrfEvents || city != domain.Perm {
		t.Fatalf("got %s %s %v", source, city, err)
	}
	for _, args := range [][]string{nil, {"mkrf_events"}, {"synthetic", "perm"}, {"kudago", "spb"}, {"osm", "perm", "extra"}} {
		if _, _, err := parseRematerializeArgs(args); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}

func TestSourceCityUsageNamesTheCommand(t *testing.T) {
	_, _, err := parseSourceCityArgs("replay", nil)
	if err == nil || err.Error() != "usage: syncer replay <kudago|mkrf_events|osm> <moscow|perm>" {
		t.Fatalf("got %v", err)
	}
}
