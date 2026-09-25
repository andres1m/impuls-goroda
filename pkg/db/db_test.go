package db

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/pkg/config"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

func TestHookFailureReleasesPool(t *testing.T) {
	c, err := NewDb(zap.NewNop(), config.Database{Host: "postgres://localhost/test", MaxConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("hook failed")
	c.AddAfterRun(func(context.Context, *pgxpool.Pool) error { return sentinel })
	if err := c.Init(context.Background()); !errors.Is(err, sentinel) {
		t.Fatalf("error = %v", err)
	}
	if c.Pool != nil {
		t.Fatal("failed init retained pool")
	}
	if err := c.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestStopBeforeInit(t *testing.T) {
	c, err := NewDb(zap.NewNop(), config.Database{Host: "postgres://localhost/test", MaxConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.HealthCheck(context.Background()); err == nil {
		t.Fatal("uninitialized database healthy")
	}
}

func TestStopHonorsDeadlineWithBorrowedConnection(t *testing.T) {
	c, err := NewDb(zap.NewNop(), config.Database{Host: "postgres://localhost/test?sslmode=disable", MaxConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	c.cfg.ConnConfig.DialFunc = func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			backend := pgproto3.NewBackend(server, server)
			if _, err := backend.ReceiveStartupMessage(); err != nil {
				return
			}
			backend.Send(&pgproto3.AuthenticationOk{})
			backend.Send(&pgproto3.ParameterStatus{Name: "server_version", Value: "17.0"})
			backend.Send(&pgproto3.BackendKeyData{ProcessID: 1, SecretKey: []byte{0, 0, 0, 1}})
			backend.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
			if err := backend.Flush(); err != nil {
				return
			}
			for {
				if _, err := backend.Receive(); err != nil {
					return
				}
			}
		}()
		return client, nil
	}
	if err := c.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := c.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stopCancel()
	done := make(chan error, 1)
	go func() { done <- c.Stop(stopCtx) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("stop error = %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Stop ignored deadline")
	}
	conn.Release()
	if err := c.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}
