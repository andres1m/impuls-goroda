package lifecycle

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

var (
	sessionID = uuid.MustParse("0b5c3f6e-2d1a-4c8e-9f3b-7a6d5e4c3b2a")
	eventID   = uuid.MustParse("6f1d2c3b-4a59-4e78-8b90-1c2d3e4f5a6b")
	recordID  = uuid.MustParse("a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d")
)

func sample() Cancellation {
	return Cancellation{
		Version:               SchemaVersion,
		ChangeID:              ChangeID(sessionID, 7),
		City:                  domain.Perm,
		CatalogRevision:       7,
		EventID:               eventID,
		SessionID:             sessionID,
		OldAvailabilityStatus: "unknown",
		NewAvailabilityStatus: StatusCancelled,
		SourceRecordID:        recordID,
		DataMode:              domain.Live,
		ObservedAt:            time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC),
		Reason:                ReasonSourceRemoved,
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	c := sample()
	data, err := Encode(&c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if !got.ObservedAt.Equal(c.ObservedAt) {
		t.Fatalf("observed at %s, want %s", got.ObservedAt, c.ObservedAt)
	}
	got.ObservedAt = c.ObservedAt
	if got != c {
		t.Fatalf("got %+v, want %+v", got, c)
	}
}

func TestValidateRejectsIncompleteCancellations(t *testing.T) {
	for name, mutate := range map[string]func(*Cancellation){
		"other schema":        func(c *Cancellation) { c.Version = 2 },
		"no change":           func(c *Cancellation) { c.ChangeID = uuid.Nil },
		"no session":          func(c *Cancellation) { c.SessionID = uuid.Nil },
		"no event":            func(c *Cancellation) { c.EventID = uuid.Nil },
		"no source record":    func(c *Cancellation) { c.SourceRecordID = uuid.Nil },
		"no city":             func(c *Cancellation) { c.City = "" },
		"no revision":         func(c *Cancellation) { c.CatalogRevision = 0 },
		"not a cancellation":  func(c *Cancellation) { c.NewAvailabilityStatus = "unknown" },
		"already cancelled":   func(c *Cancellation) { c.OldAvailabilityStatus = StatusCancelled },
		"no previous status":  func(c *Cancellation) { c.OldAvailabilityStatus = "" },
		"no data mode":        func(c *Cancellation) { c.DataMode = "" },
		"no observation time": func(c *Cancellation) { c.ObservedAt = time.Time{} },
		"no reason":           func(c *Cancellation) { c.Reason = "" },
	} {
		c := sample()
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if _, err := Encode(&c); err == nil {
			t.Errorf("%s: encoded", name)
		}
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	if _, err := Decode([]byte("{")); err == nil {
		t.Fatal("broken JSON accepted")
	}
	if _, err := Decode([]byte(`{"version":1}`)); err == nil {
		t.Fatal("incomplete payload accepted")
	}
}

func TestChangeIDIsStablePerSessionAndRevision(t *testing.T) {
	if ChangeID(sessionID, 7) != ChangeID(sessionID, 7) {
		t.Fatal("change id is not deterministic")
	}
	if ChangeID(sessionID, 7) == ChangeID(sessionID, 8) {
		t.Fatal("a later revision reuses the change id")
	}
	if ChangeID(sessionID, 7) == ChangeID(eventID, 7) {
		t.Fatal("another session shares the change id")
	}
}

func TestKeyIsCityAndSession(t *testing.T) {
	c := sample()
	if got, want := string(c.Key()), "perm:"+sessionID.String(); got != want {
		t.Fatalf("key %q, want %q", got, want)
	}
}
