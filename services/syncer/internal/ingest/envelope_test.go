package ingest

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

func validEnvelope() Envelope {
	return Envelope{
		Version:       EnvelopeVersion,
		RawIngestID:   "0b5c3f6e-2d1a-4c8e-9f3b-7a6d5e4c3b2a",
		Source:        domain.KudaGo,
		City:          domain.Moscow,
		ExternalID:    "event:226287",
		ContentHash:   "ab12",
		FetchedAt:     time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC),
		DataMode:      domain.Live,
		SchemaVersion: "kudago-api-1.4",
	}
}

func TestEnvelopeKeyUsesCityAndExternalID(t *testing.T) {
	if got := validEnvelope().Key(); got != "moscow:event:226287" {
		t.Fatalf("key = %q", got)
	}
}

func TestDecodeEnvelopeRoundTrip(t *testing.T) {
	data, err := json.Marshal(validEnvelope())
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeEnvelope(data)
	if err != nil || got != validEnvelope() {
		t.Fatalf("decoded %+v, %v", got, err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"version", "raw_ingest_id", "source", "city", "external_id", "content_hash", "fetched_at", "data_mode", "schema_version"} {
		if _, ok := fields[name]; !ok {
			t.Fatalf("field %s missing in %s", name, data)
		}
	}
}

func TestEnvelopeValidateRejects(t *testing.T) {
	cases := map[string]func(*Envelope){
		"version":     func(e *Envelope) { e.Version = 2 },
		"id":          func(e *Envelope) { e.RawIngestID = "not-a-uuid" },
		"source":      func(e *Envelope) { e.Source = "" },
		"city":        func(e *Envelope) { e.City = "spb" },
		"external_id": func(e *Envelope) { e.ExternalID = "" },
		"data_mode":   func(e *Envelope) { e.DataMode = "cached" },
	}
	for name, mutate := range cases {
		e := validEnvelope()
		mutate(&e)
		if err := e.Validate(); err == nil {
			t.Errorf("%s: accepted %+v", name, e)
		}
	}
	if err := validEnvelope().Validate(); err != nil {
		t.Fatalf("valid envelope rejected: %v", err)
	}
}

func TestDecodeEnvelopeRejectsGarbage(t *testing.T) {
	for _, data := range []string{``, `not json`, `{"version":1}`} {
		if _, err := DecodeEnvelope([]byte(data)); err == nil {
			t.Errorf("accepted %q", data)
		}
	}
}
