// Package catalogevent is the message syncer publishes when a city's catalog gets a new revision,
// so that services caching the catalog know their copy is out of date.
package catalogevent

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

const (
	// Channel is the Redis Pub/Sub channel the message goes to.
	Channel = "catalog:revision"
	// Destination and EventType mark the message in syncer's delivery outbox.
	Destination = "redis"
	EventType   = "catalog.revision"
)

type Reason string

const (
	ReasonSeed   Reason = "seed"
	ReasonIngest Reason = "ingest"
	ReasonUrgent Reason = "urgent"
)

type Invalidation struct {
	City            string `json:"city"`
	CatalogRevision int64  `json:"catalog_revision"`
	Reason          Reason `json:"reason"`
	// Entities the publication touched; empty when it rebuilt the city.
	Sessions []string `json:"sessions,omitempty"`
	Places   []string `json:"places,omitempty"`
	// When the catalog was published, not when the message was sent.
	PublishedAt time.Time `json:"published_at"`
}

func (m Invalidation) Validate() error {
	if m.City == "" {
		return errors.New("city is required")
	}
	if m.CatalogRevision <= 0 {
		return errors.New("catalog revision must be positive")
	}
	if m.Reason != ReasonSeed && m.Reason != ReasonIngest && m.Reason != ReasonUrgent {
		return fmt.Errorf("unknown reason %q", m.Reason)
	}
	if m.PublishedAt.IsZero() {
		return errors.New("publish time is required")
	}
	if slices.Contains(m.Sessions, "") || slices.Contains(m.Places, "") {
		return errors.New("entity ids must not be empty")
	}
	return nil
}

func Encode(m Invalidation) ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	m.PublishedAt = m.PublishedAt.UTC()
	return json.Marshal(m)
}

// Decode accepts fields it does not know, so a newer producer does not break older consumers.
func Decode(data []byte) (Invalidation, error) {
	var m Invalidation
	if err := json.Unmarshal(data, &m); err != nil {
		return Invalidation{}, fmt.Errorf("decode catalog invalidation: %w", err)
	}
	if err := m.Validate(); err != nil {
		return Invalidation{}, fmt.Errorf("decode catalog invalidation: %w", err)
	}
	return m, nil
}
