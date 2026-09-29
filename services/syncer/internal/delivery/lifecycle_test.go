package delivery

import (
	"context"
	"net"
	"testing"
	"time"

	pb "github.com/andres1m/impuls-goroda/proto/gateway/v1"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
)

type captureLifecycleServer struct {
	pb.UnimplementedLifecycleServiceServer
	secret chan string
}

type eventSenderFunc func(context.Context, *Item) error

func (f eventSenderFunc) Send(ctx context.Context, item *Item) error { return f(ctx, item) }

func TestEventSenderKeepsKafkaTopicsSeparate(t *testing.T) {
	var received []string
	router := EventSender{
		LifecycleEventType: eventSenderFunc(func(_ context.Context, _ *Item) error {
			received = append(received, "lifecycle")
			return nil
		}),
		ingest.DeadLetterEventType: eventSenderFunc(func(_ context.Context, _ *Item) error {
			received = append(received, "dead_letter")
			return nil
		}),
	}
	for _, eventType := range []string{LifecycleEventType, ingest.DeadLetterEventType} {
		if err := router.Send(context.Background(), &Item{EventType: eventType}); err != nil {
			t.Fatal(err)
		}
	}
	if len(received) != 2 || received[0] != "lifecycle" || received[1] != "dead_letter" {
		t.Fatalf("routed events: %v", received)
	}
	if err := router.Send(context.Background(), &Item{EventType: "unknown"}); err == nil {
		t.Fatal("unknown kafka event type was accepted")
	}
}

func (s *captureLifecycleServer) DeliverCatalogLifecycle(
	ctx context.Context, request *pb.DeliverCatalogLifecycleRequest,
) (*pb.DeliverCatalogLifecycleResponse, error) {
	values, _ := metadata.FromIncomingContext(ctx)
	if got := values.Get("x-lifecycle-secret"); len(got) == 1 {
		s.secret <- got[0]
	}
	return &pb.DeliverCatalogLifecycleResponse{DeliveryId: request.DeliveryId}, nil
}

func TestDecodeLifecycleChecksOutboxIdentity(t *testing.T) {
	id := uuid.New()
	session := uuid.New()
	request := &pb.DeliverCatalogLifecycleRequest{DeliveryId: id[:], SessionId: session[:], City: "perm"}
	payload, err := protojson.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	item := &Item{ID: id, EventType: "catalog.lifecycle.v1", Payload: payload}
	if _, err := decodeLifecycle(item); err != nil {
		t.Fatal(err)
	}
	item.ID = uuid.New()
	if _, err := decodeLifecycle(item); err == nil {
		t.Fatal("different outbox id was accepted")
	}
	item.ID = id
	item.EventType = "catalog.revision"
	if _, err := decodeLifecycle(item); err == nil {
		t.Fatal("different event type was accepted")
	}
}

func TestGatewaySenderUsesServiceCredentialAndChecksAck(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	capture := &captureLifecycleServer{secret: make(chan string, 1)}
	pb.RegisterLifecycleServiceServer(server, capture)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	sender, err := NewGatewaySender(listener.Addr().String(), "private-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sender.Close() }()
	id := uuid.New()
	session := uuid.New()
	payload, err := protojson.Marshal(&pb.DeliverCatalogLifecycleRequest{
		DeliveryId: id[:], SessionId: session[:], City: "perm",
	})
	if err != nil {
		t.Fatal(err)
	}
	item := &Item{ID: id, EventType: "catalog.lifecycle.v1", Payload: payload}
	if err := sender.Send(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-capture.secret:
		if got != "private-secret" {
			t.Fatal("gateway received a different service credential")
		}
	case <-time.After(time.Second):
		t.Fatal("gateway did not receive service credential")
	}
}
