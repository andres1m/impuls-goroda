package optimizerclient

import (
	"context"
	"errors"

	"github.com/andres1m/impuls-goroda/pkg/config"
	"github.com/andres1m/impuls-goroda/pkg/rpc"
	"github.com/andres1m/impuls-goroda/pkg/svc"
	optimizerv1 "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	"go.uber.org/zap"
)

type Client struct {
	transport *rpc.Client
	optimizer optimizerv1.OptimizerServiceClient
}

func New(log *zap.Logger, cfg *config.GRPCClient) *Client {
	return &Client{transport: rpc.NewClient("optimizer", log, cfg)}
}

func (c *Client) Name() string { return c.transport.Name() }

func (c *Client) DependsOn() []string { return c.transport.DependsOn() }

func (c *Client) Init(ctx context.Context) error {
	if err := c.transport.Init(ctx); err != nil {
		return err
	}
	c.optimizer = optimizerv1.NewOptimizerServiceClient(c.transport.GetConn())
	return nil
}

func (c *Client) HealthCheck(context.Context) error {
	if c.optimizer == nil || c.transport.GetConn() == nil {
		return errors.New("optimizer client is not initialized")
	}
	return nil
}

func (c *Client) Run(ctx context.Context) error { return c.transport.Run(ctx) }

func (c *Client) Stop(ctx context.Context) error {
	c.optimizer = nil
	return c.transport.Stop(ctx)
}

func (c *Client) RPC() optimizerv1.OptimizerServiceClient { return c.optimizer }

var _ svc.Service = (*Client)(nil)
