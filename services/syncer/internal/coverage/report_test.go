package coverage

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

func TestRenderKeepsFetchedAndSourceUpdatedApart(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	fetched := now.Add(-3 * time.Hour)
	report := Report{
		Sources: []Source{
			{Key: domain.KudaGo, City: domain.Moscow, DataModes: "live", LastSuccessAt: &fetched, LatestFetchedAt: &fetched, Applied: 5, Failed: 1},
			{Key: domain.OSM, City: domain.Perm, LastErrorCode: "timeout"},
		},
		Attributes: []Attribute{{
			City: domain.Moscow, Target: "place", Name: "title", Facts: 2, OldestFetchedAt: fetched, LatestFetchedAt: fetched,
		}},
		Catalog: []Catalog{{City: domain.Moscow, DataMode: domain.Live, Places: 3, Events: 4, UpcomingSessions: 9}},
	}
	var out bytes.Buffer
	if err := Render(&out, &report, now); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"kudago  moscow  live", "2026-10-01 09:00 (3h0m0s ago)", "0/5/1/0", "timeout", "title", "live",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("report lacks %q:\n%s", want, text)
		}
	}
	kudagoLine := lineWith(text, "kudago")
	if strings.Count(kudagoLine, "2026-10-01 09:00") != 2 {
		t.Errorf("only last success and latest fetched are known, source-updated must stay empty: %q", kudagoLine)
	}
}

func TestRenderNeverJudgesFreshness(t *testing.T) {
	now := time.Now()
	old := now.Add(-1000 * time.Hour)
	report := Report{Sources: []Source{{Key: domain.KudaGo, City: domain.Moscow, LastSuccessAt: &old}}}
	var out bytes.Buffer
	if err := Render(&out, &report, now); err != nil {
		t.Fatal(err)
	}
	for _, verdict := range []string{"stale", "fresh", "degraded"} {
		for _, line := range strings.Split(out.String(), "\n") {
			if strings.HasPrefix(line, "kudago") && strings.Contains(strings.ToLower(line), verdict) {
				t.Errorf("row carries a verdict %q: %q", verdict, line)
			}
		}
	}
}

func lineWith(text, prefix string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}
