package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	gatewayv1 "github.com/andres1m/impuls-goroda/proto/gateway/v1"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/delivery"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

type lifecycleTransition struct {
	EventID        uuid.UUID
	SessionID      uuid.UUID
	OldStatus      string
	NewStatus      string
	SourceRecordID string
	DataMode       string
	Reason         string
}

func enqueueLifecycle(
	ctx context.Context, tx pgx.Tx, city domain.City, revision int64, at time.Time, changes []lifecycleTransition,
) error {
	for _, change := range changes {
		changeID := uuid.New()
		for _, destination := range []string{"gateway", "kafka"} {
			deliveryID := uuid.New()
			message := &gatewayv1.DeliverCatalogLifecycleRequest{
				SchemaVersion: 1, DeliveryId: deliveryID[:], ChangeId: changeID[:], City: string(city),
				CatalogRevision: revision, EventId: change.EventID[:], SessionId: change.SessionID[:],
				OldAvailabilityStatus: change.OldStatus, NewAvailabilityStatus: change.NewStatus,
				SourceRecordId: change.SourceRecordID, DataMode: change.DataMode,
				ObservedAt: timestamppb.New(at), Reason: change.Reason,
			}
			payload, err := protojson.Marshal(message)
			if err != nil {
				return fmt.Errorf("encode lifecycle event: %w", err)
			}
			_, err = tx.Exec(ctx, `INSERT INTO integration.change_delivery
				(id, change_id, city, catalog_revision, destination, event_type, source_record_id, payload, state, next_attempt_at, created_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'pending',$9,$9)`,
				deliveryID, changeID, city, revision, destination, delivery.LifecycleEventType,
				change.SourceRecordID, payload, at)
			if err != nil {
				return fmt.Errorf("enqueue lifecycle event: %w", err)
			}
		}
	}
	return nil
}
