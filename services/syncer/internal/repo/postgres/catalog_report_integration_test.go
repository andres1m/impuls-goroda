package postgres

import (
	"slices"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/coverage"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/materialize"
)

func TestCoverageIntegration(t *testing.T) {
	f := newMaterializeFixture(t)
	base := time.Now().UTC().Truncate(time.Microsecond)
	reported := base.Add(-48 * time.Hour)
	for i, externalID := range []string{"node/1", "node/2", "node/3", "node/4"} {
		rec := domain.RawRecord{ExternalID: externalID, SourceURL: "https://example.test/" + externalID,
			Payload: []byte(`{"v":1}`), ContentType: "application/json"}
		if i == 0 {
			rec.SourceUpdatedAt = &reported
		}
		if _, err := f.landing.SaveRecord(f.ctx, f.sourceID, domain.Perm, domain.Live, &rec, base.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.landing.FinishRun(f.ctx, &ingest.Run{SourceID: f.sourceID, City: domain.Perm, AttemptAt: base}); err != nil {
		t.Fatal(err)
	}
	envelopes, err := f.landing.Unpublished(f.ctx, f.sourceID, domain.Perm)
	if err != nil || len(envelopes) != 4 {
		t.Fatalf("landed %d, %v", len(envelopes), err)
	}
	raws := f.pending(t, envelopes[0].RawIngestID, envelopes[2].RawIngestID, envelopes[3].RawIngestID)
	if _, _, err := f.store.Publish(f.ctx, domain.Perm, &materialize.Outcome{
		Apply:  []materialize.Normalized{{Raw: raws[0], Place: draft("node/1", "gastro", "Кофейня", "gastro_coffee")}},
		Failed: []materialize.Rejected{{Raw: raws[1], Code: "missing_name"}},
		Quarantined: []materialize.Quarantined{{
			Raw: raws[2], Reason: domain.InvalidSchema, Details: materialize.QuarantineDetails{Code: "bad_payload"},
		}},
	}, base); err != nil {
		t.Fatal(err)
	}

	report, err := NewCoverage(f.pool).Read(f.ctx, "perm", base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(report.Sources, func(s coverage.Source) bool { return s.Key == f.source })
	if i < 0 {
		t.Fatalf("source is not in the report: %+v", report.Sources)
	}
	got := report.Sources[i]
	if got.City != domain.Perm || got.DataModes != "live" || got.Pending != 1 || got.Applied != 1 || got.Failed != 1 ||
		got.Quarantined != 1 ||
		got.LastSuccessAt == nil || !got.LastSuccessAt.Equal(base) || got.LastErrorCode != "" {
		t.Fatalf("source row %+v", got)
	}
	if got.LatestFetchedAt == nil || !got.LatestFetchedAt.Equal(base.Add(3*time.Second)) {
		t.Fatalf("latest fetched %v", got.LatestFetchedAt)
	}
	if got.LatestSourceUpdatedAt == nil || !got.LatestSourceUpdatedAt.Equal(reported) {
		t.Fatalf("source updated %v must stay what the source reported, not the fetch time", got.LatestSourceUpdatedAt)
	}
	// node/2 is still pending, so materialization has passed only node/1.
	if got.MaterializedTo == nil || !got.MaterializedTo.Equal(base) {
		t.Fatalf("materialized to %v, want %v", got.MaterializedTo, base)
	}
	if slices.ContainsFunc(report.Sources, func(s coverage.Source) bool { return s.City != domain.Perm }) ||
		slices.ContainsFunc(report.Catalog, func(c coverage.Catalog) bool { return c.City != domain.Perm }) {
		t.Fatalf("a report for one city holds another: %+v", report)
	}
	j := slices.IndexFunc(report.Catalog, func(c coverage.Catalog) bool { return c.DataMode == domain.Live })
	if j < 0 || report.Catalog[j].Places < 1 {
		t.Fatalf("catalog rows %+v", report.Catalog)
	}

	if all, err := NewCoverage(f.pool).Read(f.ctx, "", base); err != nil || len(all.Sources) < len(report.Sources) {
		t.Fatalf("all cities: %v, %v", len(all.Sources), err)
	}
}
