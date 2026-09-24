package temporal

import (
	"context"
	"errors"
	"fmt"

	"github.com/andres1m/impuls-goroda/pkg/config"
	"github.com/andres1m/impuls-goroda/pkg/svc"
	"github.com/andres1m/impuls-goroda/pkg/zapadapter"
	"go.temporal.io/sdk/client"
	"go.uber.org/zap"
)

type Client struct {
	log            *zap.Logger
	temporalConf   *client.Options
	conf           *config.Temporal
	TemporalClient client.Client
}

func (c *Client) DependsOn() []string {
	return []string{"logger"}
}

func (c *Client) HealthCheck(ctx context.Context) error {
	if c.TemporalClient == nil {
		return errors.New("temporal client is not initialized")
	}
	_, err := c.TemporalClient.CheckHealth(ctx, &client.CheckHealthRequest{})
	if err != nil {
		return fmt.Errorf("temporal client is not healthy: %w", err)
	}

	c.log.Info("temporal client is healthy")

	return nil
}

func (c *Client) Init(ctx context.Context) error {
	if c.TemporalClient != nil {
		return errors.New("temporal client already initialized")
	}
	cl, err := client.DialContext(ctx, *c.temporalConf)
	if err != nil {
		return err
	}

	c.TemporalClient = cl

	c.log.Info("temporal client initialized")

	return nil
}

func (c *Client) Name() string {
	return "temporal-client"
}

func (c *Client) Run(ctx context.Context) error {
	return nil
}

func (c *Client) Stop(ctx context.Context) error {
	if c.TemporalClient != nil {
		c.TemporalClient.Close()
		c.TemporalClient = nil
	}

	return nil
}

func (c *Client) TaskQueue() string {
	return c.conf.QueueName
}

var _ svc.Service = (*Client)(nil)

func NewClient(log *zap.Logger, options *client.Options, conf *config.Temporal) *Client {
	if log == nil {
		log = zap.NewNop()
	}
	if conf == nil {
		conf = &config.Temporal{}
	}
	cfg := *conf
	var opts client.Options
	if options != nil {
		opts = *options
	}
	if cfg.HostPort != "" {
		opts.HostPort = cfg.HostPort
	}
	if cfg.Namespace != "" {
		opts.Namespace = cfg.Namespace
	}
	if opts.Logger == nil {
		opts.Logger = zapadapter.NewZapAdapter(log)
	}
	return &Client{log: log, temporalConf: &opts, conf: &cfg}
}
