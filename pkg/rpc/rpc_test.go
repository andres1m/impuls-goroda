package rpc

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/pkg/config"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	pb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestClientRejectsUnconnectedState(t *testing.T) {
	c := NewClient("missing", zap.NewNop(), &config.GRPCClient{Host: "127.0.0.1", Port: 1})
	if err := c.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer c.Stop(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := c.HealthCheck(ctx); err == nil {
		t.Fatal("unconnected client reported healthy")
	}
}
func TestServerRoundTripAndBoundedStop(t *testing.T) {
	s := NewServer("test", zap.NewNop(), &config.GRPCServer{Port: 0})
	h := health.NewServer()
	h.SetServingStatus("", pb.HealthCheckResponse_SERVING)
	s.OnInit(func(s *Server) { pb.RegisterHealthServer(s.GetServer(), h) })
	if err := s.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background()) }()
	defer s.server.Stop()
	conn, err := grpc.NewClient(s.lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithNoProxy())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cl := pb.NewHealthClient(conn)
	out, err := cl.Check(ctx, &pb.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != pb.HealthCheckResponse_SERVING {
		t.Fatal(out)
	}
	stream, err := cl.Watch(ctx, &pb.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stopCancel()
	stopped := make(chan error, 1)
	go func() { stopped <- s.Stop(stopCtx) }()
	select {
	case <-stopped:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("Stop ignored deadline")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("Serve did not stop")
	}
}

func TestStopBeforeRunReleasesListener(t *testing.T) {
	s := NewServer("rollback", zap.NewNop(), &config.GRPCServer{})
	if err := s.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.lis.Close()
	addr := s.lis.Addr().String()
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listener retained after rollback: %v", err)
	}
	listener.Close()
}

func TestClientStopIsIdempotent(t *testing.T) {
	c := NewClient("test", zap.NewNop(), &config.GRPCClient{Host: "localhost", Port: 1})
	if err := c.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := c.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}
