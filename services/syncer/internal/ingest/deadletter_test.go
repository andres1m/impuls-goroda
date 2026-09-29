package ingest

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

func payloadLetter() DeadLetter {
	return DeadLetter{
		Version:     DeadLetterVersion,
		Stage:       StagePayload,
		Reason:      domain.InvalidSchema,
		RawIngestID: "0b5c3f6e-2d1a-4c8e-9f3b-7a6d5e4c3b2a",
		Source:      domain.KudaGo,
		City:        domain.Moscow,
		ExternalID:  "event:1",
		Error:       "bad_payload",
	}
}

func envelopeLetter() DeadLetter {
	return DeadLetter{
		Version: DeadLetterVersion,
		Stage:   StageEnvelope,
		Reason:  domain.InvalidSchema,
		Source:  "unknown",
		Error:   "decode envelope: invalid character 'o' in literal null (expecting 'u')",
		Record:  []byte("not json"),
	}
}

func TestDeadLetterRoundTrip(t *testing.T) {
	for _, letter := range []DeadLetter{payloadLetter(), envelopeLetter()} {
		data, err := EncodeDeadLetter(&letter)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeDeadLetter(data)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, letter) {
			t.Fatalf("round trip %+v, want %+v", got, letter)
		}
	}
}

func TestDeadLetterKeepsTheEnvelopeAsBase64(t *testing.T) {
	letter := envelopeLetter()
	data, err := EncodeDeadLetter(&letter)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"record":"bm90IGpzb24="`) || strings.Contains(string(data), "raw_ingest_id") {
		t.Fatalf("encoded %s", data)
	}
}

func TestDeadLetterKey(t *testing.T) {
	payload, envelope := payloadLetter(), envelopeLetter()
	if !bytes.Equal(payload.Key(), []byte("moscow:event:1")) || envelope.Key() != nil {
		t.Fatalf("keys %q %q", payload.Key(), envelope.Key())
	}
}

func TestDecodeDeadLetterRejectsUnknownShapes(t *testing.T) {
	for _, data := range []string{`not json`, `{"version":2,"stage":"payload"}`, `{"version":1,"stage":"other"}`} {
		if _, err := DecodeDeadLetter([]byte(data)); err == nil {
			t.Errorf("%s accepted", data)
		}
	}
}
