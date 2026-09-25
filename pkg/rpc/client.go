package rpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/andres1m/impuls-goroda/pkg/config"
	"github.com/andres1m/impuls-goroda/pkg/svc"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	log  *zap.Logger
	cfg  *config.GRPCClient
	name string

	conn *grpc.ClientConn

	onInit []func(*Client)
}

func (c *Client) GetConn() *grpc.ClientConn {
	return c.conn
}

func (c *Client) OnInit(fn func(*Client)) {
	c.onInit = append(c.onInit, fn)
}

func (c *Client) DependsOn() []string {
	return []string{"logger"}
}

func (c *Client) HealthCheck(ctx context.Context) error {
	if c.conn == nil {
		return errors.New("grpc client is not initialized")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c.conn.Connect()
	for {
		state := c.conn.GetState()
		if state == connectivity.Ready {
			return nil
		}
		if state == connectivity.Shutdown {
			return errors.New("grpc client is shut down")
		}
		if !c.conn.WaitForStateChange(ctx, state) {
			return fmt.Errorf("grpc connection readiness: %w", ctx.Err())
		}
	}
}

func (c *Client) Init(ctx context.Context) error {
	if c.conn != nil {
		return errors.New("grpc client already initialized")
	}
	if c.cfg.Host == "" || c.cfg.Port <= 0 || c.cfg.Port > 65535 {
		return errors.New("invalid grpc client address")
	}
	opts := []grpc.DialOption{}

	if c.cfg.MaxRecvMsgSize > 0 {
		opts = append(opts, grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(c.cfg.MaxRecvMsgSize)))
	}

	if c.cfg.UseTLS {
		cert, err := tls.LoadX509KeyPair(c.cfg.TLS.ClientCertPath, c.cfg.TLS.ClientKeyPath)
		if err != nil {
			return fmt.Errorf("failed to load x509 key pair: %w", err)
		}

		caCertPool := x509.NewCertPool()

		caBytes, err := os.ReadFile(c.cfg.TLS.CaCertPath)
		if err != nil {
			return fmt.Errorf("failed to read ca cert: %w", err)
		}

		if ok := caCertPool.AppendCertsFromPEM(caBytes); !ok {
			return fmt.Errorf("failed to append ca cert")
		}

		tlsConfig := &tls.Config{
			Certificates: []tls.Certificate{cert},
			RootCAs:      caCertPool,
			MinVersion:   tls.VersionTLS13,
		}

		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	conn, err := grpc.NewClient(
		net.JoinHostPort(c.cfg.Host, strconv.Itoa(c.cfg.Port)),
		opts...,
	)
	if err != nil {
		return fmt.Errorf("failed to create grpc client: %w", err)
	}

	c.conn = conn

	for _, fn := range c.onInit {
		fn(c)
	}

	c.log.Info("grpc client started", zap.Int("port", c.cfg.Port))

	return nil
}

func (c *Client) Name() string {
	return fmt.Sprintf("grpc-client-%s", c.name)
}

func (c *Client) Run(ctx context.Context) error {
	return nil
}

func (c *Client) Stop(ctx context.Context) error {
	if c.conn == nil {
		return nil
	}
	if err := c.conn.Close(); err != nil && !errors.Is(err, grpc.ErrClientConnClosing) {
		return fmt.Errorf("failed to close grpc client: %w", err)
	}

	return nil
}

func NewClient(name string, log *zap.Logger, cfg *config.GRPCClient) *Client {
	if log == nil {
		log = zap.NewNop()
	}
	if cfg == nil {
		cfg = &config.GRPCClient{}
	}
	copied := *cfg
	return &Client{
		name: name,
		log:  log,
		cfg:  &copied,
	}
}

var _ svc.Service = (*Client)(nil)
