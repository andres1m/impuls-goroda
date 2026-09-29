package optimizerclient

import (
	"context"
	"errors"
	"strings"
	"time"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

var (
	ErrUnavailable     = errors.New("optimizer is unavailable")
	ErrBusy            = errors.New("optimizer computation limit reached")
	ErrDeadline        = errors.New("optimizer computation deadline exceeded")
	ErrCanceled        = errors.New("optimizer computation canceled")
	ErrInvalidInput    = errors.New("optimizer rejected calculation input")
	ErrMessageTooLarge = errors.New("optimizer message exceeds size limit")
	ErrInvalidResponse = errors.New("optimizer returned an invalid response")
)

type Policy struct {
	Timeout         time.Duration `yaml:"timeout"`
	MaxMessageBytes int           `yaml:"max-message-bytes"`
	MaxConcurrent   int           `yaml:"max-concurrent"`
}

func (p Policy) normalized() (Policy, error) {
	if p.Timeout == 0 {
		p.Timeout = 10 * time.Second
	}
	if p.MaxMessageBytes == 0 {
		p.MaxMessageBytes = 4 << 20
	}
	if p.MaxConcurrent == 0 {
		p.MaxConcurrent = 4
	}
	if p.Timeout < 0 || p.MaxMessageBytes < 0 || p.MaxConcurrent < 0 {
		return Policy{}, errors.New("invalid optimizer computation policy")
	}
	return p, nil
}

func (c *Client) begin(ctx context.Context, request proto.Message, requestID string) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, calculationError(err)
	}
	if c.optimizer == nil {
		return nil, nil, ErrUnavailable
	}
	if strings.TrimSpace(requestID) == "" || len(requestID) > 128 {
		return nil, nil, ErrInvalidInput
	}
	for _, character := range requestID {
		if character < 33 || character > 126 {
			return nil, nil, ErrInvalidInput
		}
	}
	if proto.Size(request) > c.policy.MaxMessageBytes {
		return nil, nil, ErrMessageTooLarge
	}
	select {
	case c.inflight <- struct{}{}:
	default:
		return nil, nil, ErrBusy
	}
	ctx, cancel := context.WithTimeout(ctx, c.policy.Timeout)
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Set("x-request-id", requestID)
	ctx = metadata.NewOutgoingContext(ctx, md)
	return ctx, func() { cancel(); <-c.inflight }, nil
}

func (c *Client) callOptions() []grpc.CallOption {
	return []grpc.CallOption{
		grpc.MaxCallRecvMsgSize(c.policy.MaxMessageBytes),
		grpc.MaxCallSendMsgSize(c.policy.MaxMessageBytes),
	}
}

func (c *Client) Optimize(ctx context.Context, request *pb.OptimizeRequest, requestID string) (*pb.OptimizeResponse, error) {
	if request == nil {
		return nil, ErrInvalidInput
	}
	ctx, finish, err := c.begin(ctx, request, requestID)
	if err != nil {
		return nil, err
	}
	defer finish()
	result, err := c.optimizer.Optimize(ctx, request, c.callOptions()...)
	if err != nil {
		return nil, calculationError(err)
	}
	if result == nil {
		return nil, ErrInvalidResponse
	}
	return result, nil
}

func (c *Client) Recompute(ctx context.Context, request *pb.RecomputeRequest, requestID string) (*pb.RecomputeResponse, error) {
	if request == nil {
		return nil, ErrInvalidInput
	}
	ctx, finish, err := c.begin(ctx, request, requestID)
	if err != nil {
		return nil, err
	}
	defer finish()
	result, err := c.optimizer.Recompute(ctx, request, c.callOptions()...)
	if err != nil {
		return nil, calculationError(err)
	}
	if result == nil {
		return nil, ErrInvalidResponse
	}
	return result, nil
}

func calculationError(err error) error {
	if errors.Is(err, context.Canceled) {
		return ErrCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrDeadline
	}
	switch status.Code(err) {
	case codes.Canceled:
		return ErrCanceled
	case codes.DeadlineExceeded:
		return ErrDeadline
	case codes.InvalidArgument:
		return ErrInvalidInput
	case codes.ResourceExhausted:
		return ErrBusy
	case codes.Internal, codes.DataLoss:
		return ErrInvalidResponse
	default:
		return ErrUnavailable
	}
}
