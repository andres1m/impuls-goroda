package grpcapi

import (
	"context"
	"testing"
	"time"

	pb "github.com/andres1m/impuls-goroda/proto/gateway/v1"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/lifecycle"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type lifecycleReceiver struct{ calls int }

func (r *lifecycleReceiver) ReceiveCatalogLifecycle(context.Context, lifecycle.Change, [32]byte) error {
	r.calls++
	return nil
}

func TestLifecycleServerRequiresServiceCredential(t *testing.T) {
	receiver := &lifecycleReceiver{}
	server := NewLifecycleServer(receiver, "private-secret")
	delivery := uuid.New()
	change := uuid.New()
	session := uuid.New()
	request := &pb.DeliverCatalogLifecycleRequest{
		SchemaVersion: 1, DeliveryId: delivery[:], ChangeId: change[:], City: "perm",
		CatalogRevision: 1, SessionId: session[:], OldAvailabilityStatus: "available",
		NewAvailabilityStatus: "cancelled", DataMode: "synthetic", Reason: "source_removed",
		ObservedAt: timestamppb.New(time.Now()),
	}
	for _, secret := range []string{"", "wrong"} {
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-lifecycle-secret", secret))
		_, err := server.DeliverCatalogLifecycle(ctx, request)
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("credential %q: code=%v", secret, status.Code(err))
		}
	}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-lifecycle-secret", "private-secret"))
	ack, err := server.DeliverCatalogLifecycle(ctx, request)
	if err != nil || ack == nil || receiver.calls != 1 {
		t.Fatalf("authorized delivery: ack=%v err=%v calls=%d", ack, err, receiver.calls)
	}
}
