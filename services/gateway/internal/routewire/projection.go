package routewire

import (
	"fmt"
	"strconv"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/google/uuid"
)

func PlanToWire(plan d.RoutePlanSnapshot) (RoutePlan, error) {
	if err := plan.Validate(); err != nil {
		return RoutePlan{}, ErrInvalidResult
	}
	out := RoutePlan{
		SchemaVersion: int64(plan.SchemaVersion), Lifecycle: string(plan.Lifecycle), ArchetypeID: plan.ArchetypeID,
		Timezone: plan.Timezone, StartAt: plan.StartAt, EndAt: plan.EndAt, Origin: coordinateWire(plan.Origin),
		Constraints: constraintsWire(plan.Constraints), CatalogRevision: strconv.FormatInt(int64(plan.CatalogRevision), 10),
		Result: string(plan.Result), Cost: summaryWire(plan.Cost), Geometry: coordinatesWire(plan.Geometry),
		Warnings: make([]Warning, 0, len(plan.Warnings)), Conflicts: make([]Conflict, 0, len(plan.Conflicts)),
		Steps: make([]RouteStep, 0, len(plan.Steps)), Legs: make([]RouteLeg, 0, len(plan.Legs)),
	}
	if plan.Destination != nil {
		point := coordinateWire(*plan.Destination)
		out.Destination = &point
	}
	for _, value := range plan.Warnings {
		warning := Warning{Code: value.Code, Scope: string(value.Scope), VisitID: idWire(value.VisitID), Message: value.Message}
		if value.LegPosition != nil {
			position := int64(*value.LegPosition)
			warning.LegPosition = &position
		}
		out.Warnings = append(out.Warnings, warning)
	}
	for _, value := range plan.Conflicts {
		conflict := Conflict{Code: value.Code, Message: value.Message, VisitIds: make([]string, 0, len(value.VisitIDs))}
		for _, id := range value.VisitIDs {
			conflict.VisitIds = append(conflict.VisitIds, uuid.UUID(id).String())
		}
		out.Conflicts = append(out.Conflicts, conflict)
	}
	for _, value := range plan.Steps {
		step := RouteStep{VisitID: uuid.UUID(value.VisitID).String(), Kind: string(value.Kind), Position: int64(value.Position),
			ArrivalAt: value.ArrivalAt, VisitStartAt: value.VisitStartAt, VisitEndAt: value.VisitEndAt, DepartureAt: value.DepartureAt,
			MinDurationSeconds: value.MinDurationSeconds, Pinned: value.Pinned, Obligation: value.Obligation,
			Participation:      ParticipationSnapshot{Status: string(value.Participation.Status), Evidence: string(value.Participation.Evidence)},
			AppliedConstraints: make([]AppliedConstraint, 0, len(value.AppliedConstraints)),
		}
		if value.Catalog != nil {
			catalog := catalogWire(*value.Catalog)
			step.Catalog = &catalog
		}
		if value.Cost != nil {
			cost := costWire(*value.Cost)
			step.Cost = &cost
		}
		if value.Lunch != nil {
			step.Lunch = &LunchMetadata{AfterVisitID: uuid.UUID(value.Lunch.AfterVisitID).String(), DurationSeconds: value.Lunch.DurationSeconds}
		}
		if value.ExternalVenue != nil {
			step.ExternalVenue = externalVenueWire(*value.ExternalVenue)
		}
		for _, constraint := range value.AppliedConstraints {
			step.AppliedConstraints = append(step.AppliedConstraints, AppliedConstraint{Code: constraint.Code, Strength: string(constraint.Strength), Outcome: string(constraint.Outcome), Message: constraint.Message})
		}
		out.Steps = append(out.Steps, step)
	}
	for _, value := range plan.Legs {
		out.Legs = append(out.Legs, RouteLeg{Position: int64(value.Position), FromKind: string(value.FromKind), ToKind: string(value.ToKind), FromVisitID: idWire(value.FromVisitID), ToVisitID: idWire(value.ToVisitID),
			DepartureAt: value.DepartureAt, ArrivalAt: value.ArrivalAt, Mode: string(value.Mode), DistanceMeters: value.DistanceMeters, Geometry: coordinatesWire(value.Geometry), Verification: string(value.Verification),
			Evidence: LegEvidence{Provider: value.Evidence.Provider, Method: value.Evidence.Method, ObservedAt: value.Evidence.ObservedAt, Mode: value.Evidence.Mode, Limitations: nonnilStrings(value.Evidence.Limitations)}, Cost: costWire(value.Cost)})
	}
	return out, nil
}

func idWire[T ~[16]byte](value *T) *string {
	if value == nil {
		return nil
	}
	id := uuid.UUID(*value).String()
	return &id
}

func textWire(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func nonnilStrings(values []string) []string { return append([]string{}, values...) }

func coordinateWire(value d.Coordinate) Coordinate {
	return Coordinate{Latitude: value.Latitude, Longitude: value.Longitude}
}

func coordinatesWire(values []d.Coordinate) []Coordinate {
	out := make([]Coordinate, 0, len(values))
	for _, value := range values {
		out = append(out, coordinateWire(value))
	}
	return out
}

func moneyWire(value d.Money) Money {
	return Money{AmountMinor: strconv.FormatInt(value.AmountMinor, 10), Currency: value.Currency}
}

func optionalMoneyWire(value *d.Money) *Money {
	if value == nil {
		return nil
	}
	money := moneyWire(*value)
	return &money
}

func minorWire(value *int64) *string {
	if value == nil {
		return nil
	}
	amount := strconv.FormatInt(*value, 10)
	return &amount
}

func unknownsWire(values []d.UnknownCostComponent) []UnknownCostComponent {
	out := make([]UnknownCostComponent, 0, len(values))
	for _, value := range values {
		out = append(out, UnknownCostComponent{Code: value.Code, Message: value.Message})
	}
	return out
}

func provenanceWire(value d.FactProvenance) PublicProvenance {
	return PublicProvenance{SourceName: value.SourceName, SourceUrl: value.SourceURL, SourceUpdatedAt: value.SourceUpdatedAt, FetchedAt: value.FetchedAt, VerifiedAt: value.VerifiedAt}
}

func costWire(value d.CostSnapshot) CostSnapshot {
	return CostSnapshot{PriceOfferID: idWire(value.PriceOfferID), Audience: textWire(value.Audience), Price: Price{Status: string(value.Price.Status), Currency: value.Price.Currency, LowerMinor: minorWire(value.Price.LowerMinor), UpperMinor: minorWire(value.Price.UpperMinor)},
		PersonalAmount: optionalMoneyWire(value.PersonalAmount), ProgramAmount: optionalMoneyWire(value.ProgramAmount), UnknownComponents: unknownsWire(value.UnknownComponents), Provenance: provenanceWire(value.Provenance)}
}

func summaryWire(value d.CostSummary) CostSummary {
	return CostSummary{KnownPersonal: moneyWire(value.KnownPersonal), KnownTransport: moneyWire(value.KnownTransport), ProgramAmount: moneyWire(value.ProgramAmount), TotalLower: optionalMoneyWire(value.TotalLower), TotalUpper: optionalMoneyWire(value.TotalUpper), UnknownComponents: unknownsWire(value.UnknownComponents), BudgetConclusion: string(value.BudgetConclusion)}
}

func catalogWire(value d.CatalogSnapshot) CatalogSnapshot {
	return CatalogSnapshot{PlaceID: idWire(value.PlaceID), EventID: idWire(value.EventID), SessionID: idWire(value.SessionID), Title: value.Title, Category: textWire(value.Category), InterestMask: fmt.Sprintf("0x%016x", value.InterestMask), Availability: string(value.Availability), RegistrationDetails: textWire(value.RegistrationDetails), AgeRequirements: textWire(value.AgeRequirements),
		SessionStartsAt: value.SessionStartsAt, SessionEndsAt: value.SessionEndsAt, SessionVersion: textWire(value.SessionVersion), DataMode: string(value.DataMode), Provenance: provenanceWire(value.Provenance)}
}

func constraintsWire(value d.RouteConstraints) RouteConstraints {
	pushkin := value.PushkinCardOnly
	out := RouteConstraints{InterestMask: fmt.Sprintf("0x%016x", value.InterestMask), ExcludedCategories: nonnilStrings(value.ExcludedCategories), LoadProfile: value.LoadProfile, Budget: Budget{Mode: string(value.Budget.Mode), Limit: optionalMoneyWire(value.Budget.Limit)}, BenefitPrograms: nonnilStrings(value.BenefitPrograms), PushkinCardOnly: &pushkin,
		MovementModes: make([]string, 0, len(value.MovementModes)), AcceptedUnknowns: make([]string, 0, len(value.AcceptedUnknowns)), SoftPreferences: nonnilStrings(value.SoftPreferences), AudienceClaims: make([]AudienceClaim, 0, len(value.AudienceClaims)), Obligations: make([]RouteObligation, 0, len(value.Obligations))}
	out.SemanticQuery = textWire(value.SemanticQuery)
	for _, mode := range value.MovementModes {
		out.MovementModes = append(out.MovementModes, string(mode))
	}
	for _, unknown := range value.AcceptedUnknowns {
		out.AcceptedUnknowns = append(out.AcceptedUnknowns, string(unknown))
	}
	for _, claim := range value.AudienceClaims {
		out.AudienceClaims = append(out.AudienceClaims, AudienceClaim{Audience: claim.Audience, Evidence: string(claim.Evidence)})
	}
	for _, obligation := range value.Obligations {
		out.Obligations = append(out.Obligations, RouteObligation{VisitID: idWire(obligation.VisitID), SessionID: idWire(obligation.SessionID), StartsAt: obligation.StartsAt, ArrivalBufferSeconds: obligation.ArrivalBufferSeconds, Participation: string(obligation.Participation)})
	}
	if value.LunchWindow != nil {
		out.LunchWindow = &LunchWindow{StartAt: value.LunchWindow.Start, EndAt: value.LunchWindow.End, MinDurationSeconds: value.LunchWindow.MinDurationSeconds}
	}
	return out
}
