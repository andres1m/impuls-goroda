package routewire

import (
	"slices"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
)

func BuildCopyRequest(routeID d.RouteID, city string, source d.RoutePlanSnapshot, origin d.Coordinate,
	destination *d.Coordinate, accepted []string) (*pb.CopyRouteRequest, ConfirmedRouteInput, error) {
	base := source.Clone()
	// Every author preference is private, including the search text, budget and
	// accessibility choices. Start with recipient defaults instead of copying any
	// field from the author's constraints.
	base.Constraints = recipientCopyConstraints(accepted)
	base.Cost.BudgetConclusion = d.BudgetNotApplicable
	request, err := buildRecomputeBase(routeID, city, base, nil)
	if err != nil {
		return nil, ConfirmedRouteInput{}, err
	}
	request.Constraints.AcceptedUnknowns = slices.Clone(accepted)
	input := ConfirmedRouteInput{City: city, Timezone: base.Timezone, StartAt: base.StartAt, EndAt: base.EndAt,
		Origin: coordinateWire(origin), Constraints: constraintsWire(base.Constraints)}
	if destination != nil {
		point := coordinateWire(*destination)
		input.Destination = &point
	}
	if _, err := input.Proto(); err != nil {
		return nil, ConfirmedRouteInput{}, err
	}
	copyRequest := &pb.CopyRouteRequest{City: city, Timezone: base.Timezone, BasePlan: request.BasePlan,
		Origin: coordinateProto(input.Origin), Constraints: request.Constraints}
	if input.Destination != nil {
		copyRequest.Destination = coordinateProto(*input.Destination)
	}
	return copyRequest, input, nil
}

func recipientCopyConstraints(accepted []string) d.RouteConstraints {
	constraints := d.RouteConstraints{
		MovementModes: []d.MovementMode{"walk"},
		LoadProfile:   "standard",
		Budget:        d.Budget{Mode: d.BudgetNone},
	}
	for _, code := range accepted {
		constraints.AcceptedUnknowns = append(constraints.AcceptedUnknowns, d.UnknownConditionCode(code))
	}
	return constraints
}

func DecodeCopyResult(input ConfirmedRouteInput, response *pb.CopyRouteResponse) (ComputedResult, error) {
	if response == nil {
		return ComputedResult{}, ErrInvalidResult
	}
	optimized := &pb.OptimizeResponse{Status: response.Status, Warnings: response.Warnings,
		Conflicts: response.Conflicts, Data: response.Data, ComputationTimeMs: response.ComputationTimeMs}
	if response.Route != nil {
		optimized.Routes = []*pb.RoutePlan{response.Route}
	}
	return DecodeResult(input, optimized)
}
