package catalogevent

import (
	"strings"
	"testing"
	"time"
)

func valid() Invalidation {
	return Invalidation{
		City: "perm", CatalogRevision: 7, Reason: ReasonUrgent,
		Sessions:    []string{"0b9f7c1e-5d7a-4f3e-9a51-6f1f0e2b8c11"},
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
	if got.City != want.City || got.CatalogRevision != want.CatalogRevision || got.Reason != want.Reason ||
		!got.PublishedAt.Equal(want.PublishedAt) || len(got.Sessions) != 1 || got.Sessions[0] != want.Sessions[0] {
		t.Fatalf("got %+v", got)
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
