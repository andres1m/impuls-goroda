package optimizerv1_test

import (
	"testing"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	"google.golang.org/protobuf/proto"
)

func TestCopyRouteContractRoundTrip(t *testing.T) {
	in := &pb.CopyRouteRequest{
		City: "perm",
		Timezone: "Asia/Yekaterinburg",
		BasePlan: &pb.RoutePlan{},
		Origin: &pb.Coordinate{Latitude: 58.01, Longitude: 56.25},
		Constraints: &pb.RouteConstraints{},
	}
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	got := &pb.CopyRouteRequest{}
	if err := proto.Unmarshal(raw, got); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(in, got) {
		t.Fatalf("copy request changed after wire round-trip: %v", got)
	}
	_ = &pb.CopyRouteResponse{Status: pb.ResultStatus_RESULT_STATUS_NO_FEASIBLE_ROUTE}
}
