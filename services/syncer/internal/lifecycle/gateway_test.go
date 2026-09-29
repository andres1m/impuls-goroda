package lifecycle

import (
	"bytes"
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	gatewayv1 "github.com/andres1m/impuls-goroda/proto/gateway/v1"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/delivery"
)

type receiver struct {
	gatewayv1.UnimplementedLifecycleServiceServer
	reply func(context.Context, *gatewayv1.DeliverCatalogLifecycleRequest) (*gatewayv1.DeliverCatalogLifecycleResponse, error)

	mu       sync.Mutex
	requests []*gatewayv1.DeliverCatalogLifecycleRequest
}

func (r *receiver) DeliverCatalogLifecycle(
	ctx context.Context,
	req *gatewayv1.DeliverCatalogLifecycleRequest,
) (*gatewayv1.DeliverCatalogLifecycleResponse, error) {
	r.mu.Lock()
	r.requests = append(r.requests, req)
	r.mu.Unlock()
	if r.reply != nil {
		return r.reply(ctx, req)
	}
	return &gatewayv1.DeliverCatalogLifecycleResponse{DeliveryId: req.GetDeliveryId()}, nil
}

func (r *receiver) received() []*gatewayv1.DeliverCatalogLifecycleRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*gatewayv1.DeliverCatalogLifecycleRequest(nil), r.requests...)
}

func startReceiver(t *testing.T, r *receiver) gatewayv1.LifecycleServiceClient {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	gatewayv1.RegisterLifecycleServiceServer(server, r)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return gatewayv1.NewLifecycleServiceClient(conn)
}

func sampleItem(t *testing.T) *delivery.Item {
	t.Helper()
	c := sample()
	payload, err := Encode(&c)
	if err != nil {
		t.Fatal(err)
	}
	return &delivery.Item{ID: uuid.New(), Destination: GatewayDestination, EventType: GatewayEventType, Payload: payload}
}

func TestGatewaySenderDeliversTheCancellation(t *testing.T) {
	rec := &receiver{}
	client := startReceiver(t, rec)
	item := sampleItem(t)
	if err := NewGatewaySender(func() gatewayv1.LifecycleServiceClient { return client }).
		Send(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	got := rec.received()
	if len(got) != 1 {
		t.Fatalf("%d requests", len(got))
	}
	c, req := sample(), got[0]
	if req.GetSchemaVersion() != 1 || !bytes.Equal(req.GetDeliveryId(), item.ID[:]) ||
		!bytes.Equal(req.GetChangeId(), c.ChangeID[:]) || !bytes.Equal(req.GetEventId(), c.EventID[:]) ||
		!bytes.Equal(req.GetSessionId(), c.SessionID[:]) || req.GetCity() != "perm" ||
		req.GetCatalogRevision() != 7 || req.GetOldAvailabilityStatus() != "unknown" ||
		req.GetNewAvailabilityStatus() != "cancelled" || req.GetSourceRecordId() != c.SourceRecordID.String() ||
		req.GetDataMode() != "live" || !req.GetObservedAt().AsTime().Equal(c.ObservedAt) ||
		req.GetReason() != "source_removed" {
		t.Fatalf("request %+v", req)
	}
}

func TestGatewaySenderRejectsAnotherDeliveryID(t *testing.T) {
	client := startReceiver(t, &receiver{reply: func(context.Context, *gatewayv1.DeliverCatalogLifecycleRequest) (*gatewayv1.DeliverCatalogLifecycleResponse, error) {
		return &gatewayv1.DeliverCatalogLifecycleResponse{DeliveryId: []byte("other")}, nil
	}})
	if err := NewGatewaySender(func() gatewayv1.LifecycleServiceClient { return client }).
		Send(context.Background(), sampleItem(t)); err == nil {
		t.Fatal("an acknowledgement of another delivery was accepted")
	}
}

func TestGatewaySenderReportsServerErrors(t *testing.T) {
	client := startReceiver(t, &receiver{reply: func(context.Context, *gatewayv1.DeliverCatalogLifecycleRequest) (*gatewayv1.DeliverCatalogLifecycleResponse, error) {
		return nil, status.Error(codes.Unimplemented, "not served")
	}})
	if err := NewGatewaySender(func() gatewayv1.LifecycleServiceClient { return client }).
		Send(context.Background(), sampleItem(t)); err == nil {
		t.Fatal("a server error was swallowed")
	}
}

func TestGatewaySenderGivesUpOnASlowServer(t *testing.T) {
	client := startReceiver(t, &receiver{reply: func(ctx context.Context, _ *gatewayv1.DeliverCatalogLifecycleRequest) (*gatewayv1.DeliverCatalogLifecycleResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	sender := NewGatewaySender(func() gatewayv1.LifecycleServiceClient { return client })
	sender.timeout = 20 * time.Millisecond
	if err := sender.Send(context.Background(), sampleItem(t)); err == nil {
		t.Fatal("a call that outlived the timeout succeeded")
	}
}

func TestGatewaySenderNeedsAConnection(t *testing.T) {
	sender := NewGatewaySender(func() gatewayv1.LifecycleServiceClient { return nil })
	if err := sender.Send(context.Background(), sampleItem(t)); err == nil {
		t.Fatal("sent without a gateway")
	}
}

func TestGatewaySenderRefusesAnUnreadablePayload(t *testing.T) {
	rec := &receiver{}
	client := startReceiver(t, rec)
	item := sampleItem(t)
	item.Payload = []byte("{")
	if err := NewGatewaySender(func() gatewayv1.LifecycleServiceClient { return client }).
		Send(context.Background(), item); err == nil {
		t.Fatal("a broken payload was sent")
	}
	if len(rec.received()) != 0 {
		t.Fatal("the gateway received a broken payload")
	}
}
