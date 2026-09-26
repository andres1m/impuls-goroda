package grpchandler

import (
	"context"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

type Planner interface {
	Optimize(ctx context.Context, req domain.OptimizeRequest) (domain.OptimizeResult, error)
	Recompute(ctx context.Context, req domain.RecomputeRequest) (domain.RecomputeResult, error)
}

type Handler struct {
	pb.UnimplementedOptimizerServiceServer

	log     *zap.Logger
	planner Planner
}

func NewHandler(log *zap.Logger, planner Planner) *Handler {
	return &Handler{log: log, planner: planner}
}

// Register serves the optimizer and the standard health service on s.
func (h *Handler) Register(s *grpc.Server) {
	pb.RegisterOptimizerServiceServer(s, h)
	hs := health.NewServer()
	hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	hs.SetServingStatus(pb.OptimizerService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(s, hs)
}
