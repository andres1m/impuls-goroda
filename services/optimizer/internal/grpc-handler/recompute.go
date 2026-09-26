package grpchandler

import (
	"context"
	"fmt"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
)

func (h *Handler) Recompute(ctx context.Context, in *pb.RecomputeRequest) (*pb.RecomputeResponse, error) {
	const method = pb.OptimizerService_Recompute_FullMethodName
	req, err := recomputeRequestFromProto(in)
	if err != nil {
		return nil, toStatus(h.log, method, err)
	}
	if err := req.Validate(); err != nil {
		return nil, toStatus(h.log, method, invalidRequest("request", err))
	}
	res, err := h.planner.Recompute(ctx, req)
	if err != nil {
		return nil, toStatus(h.log, method, err)
	}
	if err := res.Validate(); err != nil {
		return nil, toStatus(h.log, method, fmt.Errorf("planner returned an invalid result: %w", err))
	}
	if res.Candidate != nil {
		if err := res.Candidate.ValidateBudget(req.Constraints.Budget); err != nil {
			return nil, toStatus(h.log, method, fmt.Errorf("planner returned a candidate with an invalid budget: %w", err))
		}
	}
	return recomputeResponseToProto(res), nil
}
