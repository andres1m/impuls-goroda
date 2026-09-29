// Package lifecycle carries the cancellation of a catalog session from the publication that made it to
// the destinations that must react to it.
package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

const (
	SchemaVersion = 1

	GatewayDestination = "gateway"
	GatewayEventType   = "lifecycle.cancelled"
	KafkaDestination   = "kafka"
	KafkaEventType     = "lifecycle.urgent"

	StatusCancelled     = "cancelled"
	ReasonSourceRemoved = "source_removed"
)

// Recorded counts cancellations queued for delivery, once their publication has committed.
var Recorded = promauto.NewCounter(prometheus.CounterOpts{
	Name: "syncer_lifecycle_events_total",
	Help: "Session cancellations queued for delivery to gateway and Kafka.",
})

var changeNamespace = uuid.MustParse("c8f3a1d2-7b46-4e90-a5d1-2f6e8b9c0d17")

// ChangeID ties the rows of one cancellation together. It derives from the session and the revision that
// cancelled it, so a repeated publication cannot queue the same cancellation twice, while a session that
// returned and was cancelled again is a new change.
func ChangeID(sessionID uuid.UUID, revision int64) uuid.UUID {
	return uuid.NewSHA1(changeNamespace, fmt.Appendf(nil, "%s:%d", sessionID, revision))
}

type Cancellation struct {
	Version               int             `json:"version"`
	ChangeID              uuid.UUID       `json:"change_id"`
	City                  domain.City     `json:"city"`
	CatalogRevision       int64           `json:"catalog_revision"`
	EventID               uuid.UUID       `json:"event_id"`
	SessionID             uuid.UUID       `json:"session_id"`
	OldAvailabilityStatus string          `json:"old_availability_status"`
	NewAvailabilityStatus string          `json:"new_availability_status"`
	SourceRecordID        uuid.UUID       `json:"source_record_id"`
	DataMode              domain.DataMode `json:"data_mode"`
	// When the batch that cancelled the session was published, not when the source stopped listing it.
	ObservedAt time.Time `json:"observed_at"`
	Reason     string    `json:"reason"`
}

func (c *Cancellation) Validate() error {
	switch {
	case c.Version != SchemaVersion:
		return fmt.Errorf("unsupported schema version %d", c.Version)
	case c.ChangeID == uuid.Nil || c.SessionID == uuid.Nil || c.EventID == uuid.Nil || c.SourceRecordID == uuid.Nil:
		return errors.New("change, session, event and source record ids are required")
	case c.City == "" || c.CatalogRevision <= 0:
		return errors.New("city and a positive catalog revision are required")
	case c.NewAvailabilityStatus != StatusCancelled:
		return fmt.Errorf("new availability status must be %q", StatusCancelled)
	case c.OldAvailabilityStatus == "" || c.OldAvailabilityStatus == StatusCancelled:
		return errors.New("previous availability status must be known and not cancelled")
	case c.DataMode == "" || c.Reason == "" || c.ObservedAt.IsZero():
		return errors.New("data mode, reason and observation time are required")
	}
	return nil
}

func (c *Cancellation) Key() []byte {
	return []byte(string(c.City) + ":" + c.SessionID.String())
}

func Encode(c *Cancellation) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("encode cancellation: %w", err)
	}
	local := *c
	local.ObservedAt = local.ObservedAt.UTC()
	return json.Marshal(local)
}

// Decode accepts fields it does not know, so a newer producer does not break an older consumer.
func Decode(data []byte) (Cancellation, error) {
	var c Cancellation
	if err := json.Unmarshal(data, &c); err != nil {
		return Cancellation{}, fmt.Errorf("decode cancellation: %w", err)
	}
	if err := c.Validate(); err != nil {
		return Cancellation{}, fmt.Errorf("decode cancellation: %w", err)
	}
	c.ObservedAt = c.ObservedAt.UTC()
	return c, nil
}
