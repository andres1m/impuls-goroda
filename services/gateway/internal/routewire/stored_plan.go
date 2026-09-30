package routewire

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/google/uuid"
)

type storedPlan struct {
	RoutePlan
	Constraints struct {
		RouteConstraints
		SemanticQuery string `json:"semantic_query"`
	} `json:"constraints"`
	Steps []struct {
		RouteStep
		Catalog *struct {
			CatalogSnapshot
			EntranceID *string    `json:"entrance_id"`
			Provenance storedFact `json:"provenance"`
		} `json:"catalog"`
		Cost *storedCost `json:"cost"`
	} `json:"steps"`
	Legs []struct {
		RouteLeg
		Cost storedCost `json:"cost"`
	} `json:"legs"`
}

type storedFact struct {
	PublicProvenance
	SourceRecordID *string `json:"source_record_id"`
}

type storedCost struct {
	CostSnapshot
	Provenance storedFact `json:"provenance"`
}

func DecodeStoredPlan(raw []byte) (d.RoutePlanSnapshot, error) {
	return DecodeStoredPlanWithResume(raw, nil)
}

func DecodeStoredPlanWithResume(raw []byte, resume *d.RouteResume) (d.RoutePlanSnapshot, error) {
	var wire storedPlan
	if err := json.Unmarshal(raw, &wire); err != nil {
		return d.RoutePlanSnapshot{}, err
	}
	constraints, err := (ConfirmedRouteInput{Constraints: wire.Constraints.RouteConstraints}).DomainConstraints()
	if err != nil {
		return d.RoutePlanSnapshot{}, err
	}
	constraints.SemanticQuery = wire.Constraints.SemanticQuery
	m := planDecoder{}
	decode := storedDecoder{m: &m}
	out := d.RoutePlanSnapshot{SchemaVersion: decode.position(wire.SchemaVersion), Lifecycle: d.RouteLifecycle(wire.Lifecycle), ArchetypeID: wire.ArchetypeID, Timezone: wire.Timezone, StartAt: wire.StartAt, EndAt: wire.EndAt, Origin: decode.coordinate(wire.Origin), Constraints: constraints, CatalogRevision: d.CatalogRevision(decode.number(wire.CatalogRevision)), Result: d.ResultStatus(wire.Result), Geometry: decode.coordinates(wire.Geometry)}
	if wire.Destination != nil {
		point := decode.coordinate(*wire.Destination)
		out.Destination = &point
	}
	if len(wire.Conflicts) != 0 {
		return d.RoutePlanSnapshot{}, ErrInvalidResult
	}
	cost := wire.Cost
	out.Cost = d.CostSummary{KnownPersonal: decode.requiredMoney(cost.KnownPersonal), KnownTransport: decode.requiredMoney(cost.KnownTransport), ProgramAmount: decode.requiredMoney(cost.ProgramAmount), TotalLower: decode.money(cost.TotalLower), TotalUpper: decode.money(cost.TotalUpper), UnknownComponents: decode.unknowns(cost.UnknownComponents), BudgetConclusion: d.BudgetConclusion(cost.BudgetConclusion)}
	for _, value := range wire.Warnings {
		warning := d.Warning{Code: value.Code, Message: value.Message, Scope: d.WarningScope(value.Scope), VisitID: storedID[d.VisitID](&m, value.VisitID, false)}
		if value.LegPosition != nil {
			position := decode.position(*value.LegPosition)
			warning.LegPosition = &position
		}
		out.Warnings = append(out.Warnings, warning)
	}
	for i, value := range wire.Steps {
		id := storedID[d.VisitID](&m, &value.VisitID, true)
		if id == nil || value.Position != int64(i+1) {
			return d.RoutePlanSnapshot{}, ErrInvalidResult
		}
		step := d.RouteStep{VisitID: *id, Kind: d.VisitKind(value.Kind), Position: decode.position(value.Position), ArrivalAt: value.ArrivalAt, VisitStartAt: value.VisitStartAt, VisitEndAt: value.VisitEndAt, DepartureAt: value.DepartureAt, MinDurationSeconds: value.MinDurationSeconds, Pinned: value.Pinned, Obligation: value.Obligation, Participation: d.ParticipationSnapshot{Status: d.ParticipationStatus(value.Participation.Status), Evidence: d.EvidenceSource(value.Participation.Evidence)}}
		if value.Lunch != nil {
			anchor := storedID[d.VisitID](&m, &value.Lunch.AfterVisitID, true)
			if anchor != nil {
				step.Lunch = &d.LunchMetadata{AfterVisitID: *anchor, DurationSeconds: value.Lunch.DurationSeconds}
			}
		}
		if value.ExternalVenue != nil {
			venue := value.ExternalVenue
			step.ExternalVenue = &d.ExternalVenueSnapshot{Provider: venue.Provider, ExternalID: venue.ExternalID, Title: venue.Title, Address: venue.Address,
				Position: decode.coordinate(venue.Position), ObservedAt: venue.ObservedAt,
				Price: d.Price{Status: d.PriceStatus(venue.Price.Status), Currency: venue.Price.Currency,
					LowerMinor: decode.minor(venue.Price.LowerMinor), UpperMinor: decode.minor(venue.Price.UpperMinor)},
				Availability: d.AvailabilityStatus(venue.Availability), HoursVerification: d.VerificationStatus(venue.HoursVerification)}
		}
		if value.Catalog != nil {
			c := value.Catalog
			mask, err := strconv.ParseUint(strings.TrimPrefix(c.InterestMask, "0x"), 16, 64)
			if err != nil {
				return d.RoutePlanSnapshot{}, ErrInvalidResult
			}
			step.Catalog = &d.CatalogSnapshot{PlaceID: storedID[d.PlaceID](&m, c.PlaceID, false), EntranceID: storedID[d.EntranceID](&m, c.EntranceID, false), EventID: storedID[d.EventID](&m, c.EventID, false), SessionID: storedID[d.EventSessionID](&m, c.SessionID, false), Title: c.Title, Category: storedText(c.Category), InterestMask: mask, Availability: d.AvailabilityStatus(c.Availability), RegistrationDetails: storedText(c.RegistrationDetails), AgeRequirements: storedText(c.AgeRequirements), SessionStartsAt: c.SessionStartsAt, SessionEndsAt: c.SessionEndsAt, SessionVersion: storedText(c.SessionVersion), DataMode: d.DataMode(c.DataMode), Provenance: decode.fact(c.Provenance)}
		}
		if value.Cost != nil {
			cost := decode.cost(*value.Cost)
			step.Cost = &cost
		}
		for _, value := range value.AppliedConstraints {
			step.AppliedConstraints = append(step.AppliedConstraints, d.AppliedConstraint{Code: value.Code, Message: value.Message, Strength: d.ConstraintStrength(value.Strength), Outcome: d.ConstraintOutcome(value.Outcome)})
		}
		out.Steps = append(out.Steps, step)
	}
	for i, value := range wire.Legs {
		if value.Position != int64(i+1) {
			return d.RoutePlanSnapshot{}, ErrInvalidResult
		}
		out.Legs = append(out.Legs, d.RouteLeg{Position: decode.position(value.Position), FromKind: d.LegEndpointKind(value.FromKind), ToKind: d.LegEndpointKind(value.ToKind), FromVisitID: storedID[d.VisitID](&m, value.FromVisitID, false), ToVisitID: storedID[d.VisitID](&m, value.ToVisitID, false), DepartureAt: value.DepartureAt, ArrivalAt: value.ArrivalAt, Mode: d.MovementMode(value.Mode), DistanceMeters: value.DistanceMeters, Geometry: decode.coordinates(value.Geometry), Verification: d.VerificationStatus(value.Verification), Evidence: d.LegEvidence{Provider: value.Evidence.Provider, Method: value.Evidence.Method, ObservedAt: value.Evidence.ObservedAt, Mode: value.Evidence.Mode, Limitations: value.Evidence.Limitations}, Cost: decode.cost(value.Cost)})
	}
	out.Resume = resume
	if m.err != nil || out.Validate() != nil || !connectedPlan(out) {
		return d.RoutePlanSnapshot{}, ErrInvalidResult
	}
	return out.Clone(), nil
}

type storedDecoder struct{ m *planDecoder }

func storedID[T ~[16]byte](m *planDecoder, value *string, required bool) *T {
	if value == nil {
		if required {
			m.err = ErrInvalidResult
		}
		return nil
	}
	id, err := uuid.Parse(*value)
	if err != nil || id == uuid.Nil {
		m.err = ErrInvalidResult
		return nil
	}
	out := T(id)
	return &out
}

func storedText(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s storedDecoder) number(value string) int64 {
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil || number < 0 || strconv.FormatInt(number, 10) != value {
		s.m.err = ErrInvalidResult
	}
	return number
}

func (s storedDecoder) position(value int64) int {
	if value <= 0 || value > math.MaxInt32 {
		s.m.err = ErrInvalidResult
		return 0
	}
	return int(value)
}

func (s storedDecoder) coordinate(value Coordinate) d.Coordinate {
	return d.Coordinate{Longitude: value.Longitude, Latitude: value.Latitude}
}

func (s storedDecoder) coordinates(values []Coordinate) []d.Coordinate {
	out := make([]d.Coordinate, 0, len(values))
	for _, value := range values {
		out = append(out, s.coordinate(value))
	}
	return out
}

func (s storedDecoder) money(value *Money) *d.Money {
	if value == nil {
		return nil
	}
	out := &d.Money{AmountMinor: s.number(value.AmountMinor), Currency: value.Currency}
	if out.Validate() != nil {
		s.m.err = ErrInvalidResult
	}
	return out
}

func (s storedDecoder) requiredMoney(value Money) d.Money { return *s.money(&value) }

func (s storedDecoder) minor(value *string) *int64 {
	if value == nil {
		return nil
	}
	out := s.number(*value)
	return &out
}

func (s storedDecoder) unknowns(values []UnknownCostComponent) []d.UnknownCostComponent {
	out := make([]d.UnknownCostComponent, 0, len(values))
	for _, value := range values {
		out = append(out, d.UnknownCostComponent{Code: value.Code, Message: value.Message})
	}
	return out
}

func (s storedDecoder) fact(value storedFact) d.FactProvenance {
	return d.FactProvenance{SourceName: value.SourceName, SourceURL: value.SourceUrl, SourceRecordID: storedID[d.SourceRecordID](s.m, value.SourceRecordID, false), SourceUpdatedAt: value.SourceUpdatedAt, FetchedAt: value.FetchedAt, VerifiedAt: value.VerifiedAt}
}

func (s storedDecoder) cost(value storedCost) d.CostSnapshot {
	return d.CostSnapshot{PriceOfferID: storedID[d.PriceOfferID](s.m, value.PriceOfferID, false), Audience: storedText(value.Audience), Price: d.Price{Status: d.PriceStatus(value.Price.Status), Currency: value.Price.Currency, LowerMinor: s.minor(value.Price.LowerMinor), UpperMinor: s.minor(value.Price.UpperMinor)}, PersonalAmount: s.money(value.PersonalAmount), ProgramAmount: s.money(value.ProgramAmount), UnknownComponents: s.unknowns(value.UnknownComponents), Provenance: s.fact(value.Provenance)}
}
