package grpchandler

import (
	"context"
	"fmt"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
)

func (h *Handler) CopyRoute(ctx context.Context, in *pb.CopyRouteRequest) (*pb.CopyRouteResponse, error) {
	const method = pb.OptimizerService_CopyRoute_FullMethodName
	req, err := copyRequestFromProto(in)
	if err != nil {
		return nil, toStatus(h.log, method, err)
	}
	if err := req.Validate(); err != nil {
		return nil, toStatus(h.log, method, invalidRequest("request", err))
	}
	result, err := h.planner.CopyRoute(ctx, &req)
	if err != nil {
		return nil, toStatus(h.log, method, err)
	}
	if err := result.Validate(); err != nil {
		return nil, toStatus(h.log, method, fmt.Errorf("planner returned an invalid result: %w", err))
	}
	if result.Route != nil {
		if err := result.Route.ValidateBudget(req.Constraints.Budget); err != nil {
			return nil, toStatus(h.log, method, fmt.Errorf("planner returned an invalid budget: %w", err))
		}
	}
	return copyResponseToProto(&result), nil
}
