package routewire

import (
	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
)

func externalVenueWire(value d.ExternalVenueSnapshot) *ExternalLunchVenue {
	return &ExternalLunchVenue{
		Provider: value.Provider, ExternalID: value.ExternalID, Title: value.Title, Address: value.Address,
		Position: coordinateWire(value.Position), ObservedAt: value.ObservedAt,
		Price:        Price{Status: string(value.Price.Status), Currency: value.Price.Currency},
		Availability: string(value.Availability), HoursVerification: string(value.HoursVerification),
	}
}

func (m *planDecoder) externalVenue(value *pb.ExternalVenueSnapshot) *d.ExternalVenueSnapshot {
	if value == nil || value.Price == nil || value.Availability != pb.ExternalVenueAvailability_EXTERNAL_VENUE_AVAILABILITY_UNKNOWN ||
		value.HoursVerification != pb.VerificationStatus_VERIFICATION_STATUS_UNKNOWN {
		m.err = ErrInvalidResult
		return nil
	}
	out := &d.ExternalVenueSnapshot{
		Provider: value.Provider, ExternalID: value.ExternalId, Title: value.Title, Address: value.Address,
		Position: m.coordinate(value.Position), ObservedAt: m.instant(value.ObservedAt),
		Price: d.Price{Status: d.PriceStatus(m.enum(int32(value.Price.Status), pb.PriceStatus_name, "PRICE_STATUS_")),
			Currency: value.Price.Currency, LowerMinor: value.Price.LowerMinor, UpperMinor: value.Price.UpperMinor},
		Availability: d.AvailabilityUnknown, HoursVerification: d.VerificationUnknown,
	}
	if out.Validate() != nil {
		m.err = ErrInvalidResult
	}
	return out
}

func (e *planEncoder) externalVenue(value *d.ExternalVenueSnapshot) *pb.ExternalVenueSnapshot {
	if value == nil {
		return nil
	}
	return &pb.ExternalVenueSnapshot{
		Provider: value.Provider, ExternalId: value.ExternalID, Title: value.Title, Address: value.Address,
		Position: coordinateProto(coordinateWire(value.Position)), ObservedAt: e.instant(value.ObservedAt),
		Price: &pb.Price{Status: pb.PriceStatus(e.enum(string(value.Price.Status), pb.PriceStatus_value, "PRICE_STATUS_")), Currency: value.Price.Currency,
			LowerMinor: value.Price.LowerMinor, UpperMinor: value.Price.UpperMinor},
		Availability:      pb.ExternalVenueAvailability_EXTERNAL_VENUE_AVAILABILITY_UNKNOWN,
		HoursVerification: pb.VerificationStatus_VERIFICATION_STATUS_UNKNOWN,
	}
}
