package catalogevent

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func valid() Invalidation {
	return Invalidation{
		City: "perm", CatalogRevision: 7, Reason: ReasonUrgent,
		Sessions:    []string{"0b9f7c1e-5d7a-4f3e-9a51-6f1f0e2b8c11"},
		Places:      []string{"5c2d8a90-1e4b-4c7f-8d3a-2b6e9f0a1c44"},
		PublishedAt: time.Date(2026, 9, 28, 10, 0, 0, 0, time.FixedZone("perm", 5*3600)),
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	data, err := Encode(valid())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"published_at":"2026-09-28T05:00:00Z"`) {
		t.Fatalf("time is not UTC: %s", data)
	}
	got, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	want := valid()
	want.PublishedAt = want.PublishedAt.UTC()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestDecodeRejectsInvalidMessages(t *testing.T) {
	for name, body := range map[string]string{
		"broken json":      `{"city":`,
		"no city":          `{"city":"","catalog_revision":1,"reason":"seed","published_at":"2026-09-28T05:00:00Z"}`,
		"zero revision":    `{"city":"perm","catalog_revision":0,"reason":"seed","published_at":"2026-09-28T05:00:00Z"}`,
		"unknown reason":   `{"city":"perm","catalog_revision":1,"reason":"guess","published_at":"2026-09-28T05:00:00Z"}`,
		"no publish time":  `{"city":"perm","catalog_revision":1,"reason":"seed"}`,
		"empty session id": `{"city":"perm","catalog_revision":1,"reason":"seed","sessions":[""],"published_at":"2026-09-28T05:00:00Z"}`,
		"empty place id":   `{"city":"perm","catalog_revision":1,"reason":"seed","places":[""],"published_at":"2026-09-28T05:00:00Z"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode([]byte(body)); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestDecodeAcceptsFieldsOfNewerProducers(t *testing.T) {
	body := `{"city":"perm","catalog_revision":3,"reason":"ingest","published_at":"2026-09-28T05:00:00Z","source":"kudago"}`
	if _, err := Decode([]byte(body)); err != nil {
		t.Fatal(err)
	}
}

func TestEncodeRejectsInvalidMessage(t *testing.T) {
	m := valid()
	m.CatalogRevision = -1
	if _, err := Encode(m); err == nil {
		t.Fatal("accepted")
	}
}

func TestDecodeGivesUTC(t *testing.T) {
	body := `{"city":"perm","catalog_revision":3,"reason":"ingest","published_at":"2026-09-28T10:00:00+05:00"}`
	m, err := Decode([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if m.PublishedAt.Location() != time.UTC || m.PublishedAt.Hour() != 5 {
		t.Fatalf("published at %v", m.PublishedAt)
	}
}
