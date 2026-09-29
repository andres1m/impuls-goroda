package ingest

import (
	"encoding/json"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

const (
	DeadLetterVersion     = 1
	DeadLetterDestination = "kafka"
	DeadLetterEventType   = "dlq.integration.raw"
)

type DeadLetterStage string

const (
	StageEnvelope DeadLetterStage = "envelope"
	StagePayload  DeadLetterStage = "payload"
)

// SchemaMismatch counts raw data the syncer cannot read, whether the envelope or the source payload.
var SchemaMismatch = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "ingestion_schema_mismatch_total",
	Help: "Raw envelopes and payloads that could not be decoded or validated.",
}, []string{"source"})

// DeadLetter tells a person which raw data was set aside and why. An unreadable envelope has no raw
// record to point to, so it travels in Record as it arrived.
type DeadLetter struct {
	Version     int                     `json:"version"`
	Stage       DeadLetterStage         `json:"stage"`
	Reason      domain.QuarantineReason `json:"reason"`
	RawIngestID string                  `json:"raw_ingest_id,omitempty"`
	Source      domain.SourceKey        `json:"source,omitempty"`
	City        domain.City             `json:"city,omitempty"`
	ExternalID  string                  `json:"external_id,omitempty"`
	Error       string                  `json:"error"`
	Record      []byte                  `json:"record,omitempty"`
}

// Key partitions the letter like the raw record it is about.
func (d *DeadLetter) Key() []byte {
	if d.City == "" || d.ExternalID == "" {
		return nil
	}
	return []byte(string(d.City) + ":" + d.ExternalID)
}

func EncodeDeadLetter(d *DeadLetter) ([]byte, error) {
	data, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("encode dead letter: %w", err)
	}
	return data, nil
}

func DecodeDeadLetter(data []byte) (DeadLetter, error) {
	var d DeadLetter
	if err := json.Unmarshal(data, &d); err != nil {
		return DeadLetter{}, fmt.Errorf("decode dead letter: %w", err)
	}
	if d.Version != DeadLetterVersion {
		return DeadLetter{}, fmt.Errorf("unsupported dead letter version %d", d.Version)
	}
	switch d.Stage {
	case StageEnvelope, StagePayload:
		return d, nil
	}
	return DeadLetter{}, fmt.Errorf("unknown dead letter stage %q", d.Stage)
}
