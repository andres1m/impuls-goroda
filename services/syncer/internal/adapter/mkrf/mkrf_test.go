package mkrf

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

func TestFetchSplitsEmbeddedDatasetByCity(t *testing.T) {
	for city, want := range map[domain.City]int{domain.Moscow: 156, domain.Perm: 7} {
		batch, err := New().Fetch(context.Background(), city, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(batch.Records) != want || batch.Skipped != 0 {
			t.Fatalf("%s: %d records, %d skipped; want %d", city, len(batch.Records), batch.Skipped, want)
		}
		if string(batch.Cursor) != `{"dataset_version":12}` {
			t.Fatalf("cursor = %s", batch.Cursor)
		}
		seen := map[string]bool{}
		for _, r := range batch.Records {
			if !strings.HasPrefix(r.ExternalID, "event:") || seen[r.ExternalID] {
				t.Fatalf("bad or duplicate external id %q", r.ExternalID)
			}
			seen[r.ExternalID] = true
			if r.SourceURL != datasetURL || r.ContentType != "application/json" || r.ProviderVersion != "12" ||
				r.SourceUpdatedAt == nil || !json.Valid(r.Payload) {
				t.Fatalf("bad record %s", r.ExternalID)
			}
		}
	}
}

func TestFetchKeepsRecordBytes(t *testing.T) {
	batch, err := New().Fetch(context.Background(), domain.Perm, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range batch.Records {
		if r.ExternalID == "event:6894016" {
			if !strings.Contains(string(r.Payload), "Дачная Нижняя Курья") {
				t.Fatal("payload lost the event title")
			}
			if got := r.SourceUpdatedAt.UTC().Format("2006-01-02T15:04:05"); got != "2026-04-03T06:50:01" {
				t.Fatalf("source updated at %s", got)
			}
			return
		}
	}
	t.Fatal("event 6894016 not found in Perm")
}

func TestSourceIsPrepared(t *testing.T) {
	s := New().Source()
	if s.Key != domain.MkrfEvents || s.DataMode != domain.Prepared || s.AccessMode != domain.AccessExport {
		t.Fatalf("source = %+v", s)
	}
}
