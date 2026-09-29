package ingest

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

const EnvelopeVersion = 1

// Envelope announces a stored raw_ingest row; the payload itself stays in the landing zone.
type Envelope struct {
	Version       int              `json:"version"`
	RawIngestID   string           `json:"raw_ingest_id"`
	Source        domain.SourceKey `json:"source"`
	City          domain.City      `json:"city"`
	ExternalID    string           `json:"external_id"`
	ContentHash   string           `json:"content_hash"`
	FetchedAt     time.Time        `json:"fetched_at"`
	DataMode      domain.DataMode  `json:"data_mode"`
	SchemaVersion string           `json:"schema_version"`
}

func (e *Envelope) Key() string {
	return string(e.City) + ":" + e.ExternalID
}

func (e *Envelope) Validate() error {
	if e.Version != EnvelopeVersion {
		return fmt.Errorf("unsupported envelope version %d", e.Version)
	}
	if err := uuid.Validate(e.RawIngestID); err != nil {
		return fmt.Errorf("raw_ingest_id: %w", err)
	}
	if e.Source == "" {
		return errors.New("source is empty")
	}
	if _, err := domain.ParseCity(string(e.City)); err != nil {
		return err
	}
	if e.ExternalID == "" {
		return errors.New("external_id is empty")
	}
	switch e.DataMode {
	case domain.Live, domain.Prepared, domain.Synthetic:
		return nil
	}
	return fmt.Errorf("unknown data_mode %q", e.DataMode)
}

func DecodeEnvelope(data []byte) (Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(data, &e); err != nil {
		return Envelope{}, fmt.Errorf("decode envelope: %w", err)
	}
	if err := e.Validate(); err != nil {
		return Envelope{}, err
	}
	return e, nil
}
