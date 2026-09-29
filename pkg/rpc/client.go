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
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const defaultDialTimeout = 5 * time.Second

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
	ctx, cancel := context.WithTimeout(ctx, defaultDialTimeout)
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
	opts := []grpc.DialOption{
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		grpc.WithChainUnaryInterceptor(clientTelemetry),
	}

	if c.cfg.MaxRecvMsgSize > 0 {
		opts = append(opts, grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(c.cfg.MaxRecvMsgSize)))
	}

	transport, err := clientTransportCredentials(c.cfg)
	if err != nil {
		return err
	}
	opts = append(opts, grpc.WithTransportCredentials(transport))

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
	return "grpc-client-" + c.name
}

func (c *Client) Run(ctx context.Context) error {
	return nil
}

func clientTransportCredentials(cfg *config.GRPCClient) (credentials.TransportCredentials, error) {
	if !cfg.UseTLS {
		return insecure.NewCredentials(), nil
	}
	cert, err := tls.LoadX509KeyPair(cfg.TLS.ClientCertPath, cfg.TLS.ClientKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load x509 key pair: %w", err)
	}
	caBytes, err := os.ReadFile(cfg.TLS.CaCertPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read ca cert: %w", err)
	}
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caBytes) {
		return nil, errors.New("failed to append ca cert")
	}
	return credentials.NewTLS(
		&tls.Config{Certificates: []tls.Certificate{cert}, RootCAs: caCertPool, MinVersion: tls.VersionTLS13},
	), nil
}

func (c *Client) Stop(ctx context.Context) error {
	if c.conn == nil {
		return nil
	}
	if err := c.conn.Close(); err != nil && status.Code(err) != codes.Canceled {
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
