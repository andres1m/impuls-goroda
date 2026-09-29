package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	gatewayv1 "github.com/andres1m/impuls-goroda/proto/gateway/v1"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/delivery"
)

// gatewayTimeout stays well below the outbox lease: a call outliving the lease cannot record its failure,
// and the row returns at once instead of after a backoff.
const gatewayTimeout = 10 * time.Second

// GatewaySender tells gateway a session was cancelled. Gateway acknowledges after it has stored the
// cancellation, and answers a repeated delivery with the same acknowledgement.
type GatewaySender struct {
	// The connection exists only once its component has started.
	client  func() gatewayv1.LifecycleServiceClient
	timeout time.Duration
}

func NewGatewaySender(client func() gatewayv1.LifecycleServiceClient) *GatewaySender {
	return &GatewaySender{client: client, timeout: gatewayTimeout}
}

func (s *GatewaySender) Send(ctx context.Context, item *delivery.Item) error {
	c, err := Decode(item.Payload)
	if err != nil {
		return fmt.Errorf("outbox cancellation %s: %w", item.ID, err)
	}
	client := s.client()
	if client == nil {
		return errors.New("gateway is not connected")
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	resp, err := client.DeliverCatalogLifecycle(ctx, &gatewayv1.DeliverCatalogLifecycleRequest{
		SchemaVersion:         SchemaVersion,
		DeliveryId:            item.ID[:],
		ChangeId:              c.ChangeID[:],
		City:                  string(c.City),
		CatalogRevision:       c.CatalogRevision,
		EventId:               c.EventID[:],
		SessionId:             c.SessionID[:],
		OldAvailabilityStatus: c.OldAvailabilityStatus,
		NewAvailabilityStatus: c.NewAvailabilityStatus,
		SourceRecordId:        c.SourceRecordID.String(),
		DataMode:              string(c.DataMode),
		ObservedAt:            timestamppb.New(c.ObservedAt),
		Reason:                c.Reason,
	})
	if err != nil {
		return fmt.Errorf("deliver cancellation %s to gateway: %w", item.ID, err)
	}
	if !bytes.Equal(resp.GetDeliveryId(), item.ID[:]) {
		return fmt.Errorf("gateway acknowledged another delivery than %s", item.ID)
	}
	return nil
}

var _ delivery.Sender = (*GatewaySender)(nil)
