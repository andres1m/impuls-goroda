package grpchandler

import (
	"context"
	"fmt"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
)

func (h *Handler) Optimize(ctx context.Context, in *pb.OptimizeRequest) (*pb.OptimizeResponse, error) {
	const method = pb.OptimizerService_Optimize_FullMethodName
	req, err := optimizeRequestFromProto(in)
	if err != nil {
		return nil, toStatus(h.log, method, err)
	}
	if err := req.Validate(); err != nil {
		return nil, toStatus(h.log, method, invalidRequest("request", err))
	}
	res, err := h.planner.Optimize(ctx, req)
	if err != nil {
		return nil, toStatus(h.log, method, err)
	}
	if err := res.Validate(); err != nil {
		return nil, toStatus(h.log, method, fmt.Errorf("planner returned an invalid result: %w", err))
	}
	for i, route := range res.Routes {
		if err := route.ValidateBudget(req.Constraints.Budget); err != nil {
			return nil, toStatus(h.log, method, fmt.Errorf("planner returned route %d with an invalid budget: %w", i+1, err))
		}
	}
	return optimizeResponseToProto(res), nil
}
