package grpcapi

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	pb "github.com/andres1m/impuls-goroda/proto/gateway/v1"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/lifecycle"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type Receiver interface {
	ReceiveCatalogLifecycle(context.Context, lifecycle.Change, [32]byte) error
}

type LifecycleServer struct {
	pb.UnimplementedLifecycleServiceServer
	receiver Receiver
	secret   string
	slots    chan struct{}
}

const lifecycleConcurrentRequests = 4

var errInvalidServiceCredential = status.Error(codes.Unauthenticated, "invalid service credential")

func NewLifecycleServer(receiver Receiver, secret string) *LifecycleServer {
	return &LifecycleServer{receiver: receiver, secret: secret, slots: make(chan struct{}, lifecycleConcurrentRequests)}
}

func (s *LifecycleServer) DeliverCatalogLifecycle(ctx context.Context, request *pb.DeliverCatalogLifecycleRequest) (*pb.DeliverCatalogLifecycleResponse, error) {
	values, _ := metadata.FromIncomingContext(ctx)
	provided := values.Get("x-lifecycle-secret")
	if len(provided) != 1 || s.secret == "" || subtle.ConstantTimeCompare([]byte(provided[0]), []byte(s.secret)) != 1 {
		return nil, fmt.Errorf("lifecycle authorization: %w", errInvalidServiceCredential)
	}
	change, hash, err := lifecycle.Decode(request)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid catalog lifecycle change")
	}
	if s.receiver == nil {
		return nil, status.Error(codes.Unavailable, "catalog lifecycle receiver is unavailable")
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return nil, status.Error(codes.ResourceExhausted, "catalog lifecycle receiver is busy")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.receiver.ReceiveCatalogLifecycle(ctx, change, hash); err != nil {
		if ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		if errors.Is(err, postgres.ErrLifecycleConflict) {
			return nil, status.Error(codes.AlreadyExists, "delivery identifier has different content")
		}
		if errors.Is(err, postgres.ErrNotFound) || errors.Is(err, lifecycle.ErrInvalidChange) {
			return nil, status.Error(codes.InvalidArgument, "invalid catalog lifecycle change")
		}
		return nil, status.Error(codes.Unavailable, "catalog lifecycle change could not be committed")
	}
	return &pb.DeliverCatalogLifecycleResponse{DeliveryId: append([]byte(nil), change.DeliveryID[:]...)}, nil
}
