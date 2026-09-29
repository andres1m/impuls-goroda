package main

import (
	"context"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/pkg/config"
	"github.com/andres1m/impuls-goroda/pkg/rpc"
)

func TestGatewayConnStaysHealthyWhileGatewayIsDown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn := gatewayConn{rpc.NewClient("gateway", nil, &config.GRPCClient{Host: "127.0.0.1", Port: 1})}
	if err := conn.Init(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Stop(ctx) }()
	if err := conn.HealthCheck(ctx); err != nil {
		t.Fatalf("syncer would refuse to start without gateway: %v", err)
	}
}
