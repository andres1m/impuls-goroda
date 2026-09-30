package grpchandler

import (
	"testing"
	"time"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func TestLunchTriggerAndExternalStepMapping(t *testing.T) {
	in := pbRecomputeRequest()
	in.Trigger = &pb.RecomputeRequest_Lunch{Lunch: &pb.LunchTrigger{SchemaVersion: 1, Stops: []*pb.LunchStop{{VisitId: id(9), AfterVisitId: id(2), DurationSeconds: 2700, Venue: &pb.LunchStop_External{External: &pb.ExternalVenueSnapshot{Provider: "2gis", ExternalId: "cafe", Title: "Cafe", Position: &pb.Coordinate{Longitude: 56.2, Latitude: 58}, ObservedAt: ts(9, 0), Price: &pb.Price{Status: pb.PriceStatus_PRICE_STATUS_UNKNOWN, Currency: "RUB"}, Availability: pb.ExternalVenueAvailability_EXTERNAL_VENUE_AVAILABILITY_UNKNOWN, HoursVerification: pb.VerificationStatus_VERIFICATION_STATUS_UNKNOWN}}}}}}
	out, err := recomputeRequestFromProto(in)
	if err != nil {
		t.Fatal(err)
	}
	lunch, ok := out.Trigger.(domain.LunchTrigger)
	if !ok || len(lunch.Stops) != 1 || lunch.Stops[0].External.ExternalID != "cafe" {
		t.Fatalf("trigger: %+v", out.Trigger)
	}
	step := domain.Step{VisitID: domain.VisitID{9}, Kind: domain.StepExternalLunch, Lunch: &domain.LunchMetadata{AfterVisitID: domain.VisitID{2}, Duration: 45 * time.Minute}, ExternalVenue: lunch.Stops[0].External, Position: 3, ArrivalAt: at(11, 0), VisitStartAt: at(11, 5), VisitEndAt: at(11, 50), DepartureAt: at(11, 50), MinDuration: 45 * time.Minute, Participation: domain.Participation{Status: domain.ParticipationNotRequired, Evidence: domain.EvidenceNone}}
	mapped := stepToProto(&step)
	r := &reader{}
	back := stepFromProto(r, "step", mapped)
	if err := r.result(); err != nil {
		t.Fatal(err)
	}
	if back.Lunch == nil || back.Lunch.Duration != step.Lunch.Duration || back.ExternalVenue.ExternalID != "cafe" {
		t.Fatalf("roundtrip: %+v", back)
	}
}

func TestLunchWireRejectsUnsupportedVersion(t *testing.T) {
	in := pbRecomputeRequest()
	in.Trigger = &pb.RecomputeRequest_Lunch{Lunch: &pb.LunchTrigger{SchemaVersion: 2}}
	_, err := recomputeRequestFromProto(in)
	if err == nil || err.Error() != "lunch.schema_version: has an unsupported value" {
		t.Fatalf("error: %v", err)
	}
}
