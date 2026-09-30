package sharewire

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	w "github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
)

type SharedRoute struct {
	Revision    string         `json:"revision"`
	City        string         `json:"city"`
	Timezone    string         `json:"timezone"`
	StartAt     time.Time      `json:"start_at"`
	EndAt       time.Time      `json:"end_at"`
	Result      string         `json:"result"`
	ArchetypeID string         `json:"archetype_id"`
	Cost        w.CostSummary  `json:"cost"`
	Warnings    []w.Warning    `json:"warnings"`
	Issues      []w.RouteIssue `json:"issues"`
	Steps       []w.RouteStep  `json:"steps"`
	Legs        []SharedLeg    `json:"legs"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

type SharedLeg struct {
	Position       int64          `json:"position"`
	FromKind       string         `json:"from_kind"`
	ToKind         string         `json:"to_kind"`
	FromVisitID    *string        `json:"from_visit_id,omitempty"`
	ToVisitID      *string        `json:"to_visit_id,omitempty"`
	DepartureAt    time.Time      `json:"departure_at"`
	ArrivalAt      time.Time      `json:"arrival_at"`
	Mode           string         `json:"mode"`
	DistanceMeters *float64       `json:"distance_meters,omitempty"`
	Geometry       []w.Coordinate `json:"geometry,omitempty"`
	Verification   string         `json:"verification"`
	Cost           w.CostSnapshot `json:"cost"`
}

var errInvalidProjection = errors.New("invalid shared route snapshot")

func ProjectRoute(route d.Route, revision d.RouteRevision, issues []d.RouteIssue) (SharedRoute, error) {
	if route.Validate() != nil || revision.Validate() != nil || revision.RouteID != route.ID ||
		revision.Number != route.CurrentRevision || revision.Plan.Lifecycle != route.Lifecycle ||
		(revision.Plan.Result != d.ResultReady && revision.Plan.Result != d.ResultPartial) || len(revision.Plan.Conflicts) != 0 {
		return SharedRoute{}, errInvalidProjection
	}
	plan := revision.Plan.Clone()
	if plan.Resume != nil && (plan.Resume.LegPosition < 1 || plan.Resume.LegPosition > len(plan.Legs) ||
		plan.Resume.Position.Validate() != nil || plan.Resume.DepartureAt.IsZero()) {
		return SharedRoute{}, errInvalidProjection
	}
	out := SharedRoute{Revision: strconv.FormatInt(int64(revision.Number), 10), City: route.City,
		Timezone: plan.Timezone, StartAt: plan.StartAt, EndAt: plan.EndAt, Result: string(plan.Result),
		ArchetypeID: plan.ArchetypeID, Cost: summary(plan.Cost), UpdatedAt: route.UpdatedAt,
		Steps: make([]w.RouteStep, 0, len(plan.Steps)), Legs: make([]SharedLeg, 0, len(plan.Legs)),
		Warnings: make([]w.Warning, 0, len(plan.Warnings)), Issues: make([]w.RouteIssue, 0, len(issues))}
	visits := make(map[d.VisitID]bool, len(plan.Steps))
	for _, step := range plan.Steps {
		visits[step.VisitID] = true
		out.Steps = append(out.Steps, projectStep(step))
	}
	publicLegs := make(map[int]bool, len(plan.Legs))
	for _, leg := range plan.Legs {
		if leg.FromKind != d.LegVisit || leg.ToKind != d.LegVisit || (plan.Resume != nil && leg.Position == plan.Resume.LegPosition) {
			continue
		}
		publicLegs[leg.Position] = true
		geometry := make([]w.Coordinate, 0, len(leg.Geometry))
		for _, point := range leg.Geometry {
			geometry = append(geometry, w.Coordinate{Latitude: point.Latitude, Longitude: point.Longitude})
		}
		out.Legs = append(out.Legs, SharedLeg{Position: int64(leg.Position), FromKind: string(leg.FromKind),
			ToKind: string(leg.ToKind), FromVisitID: idPointer(leg.FromVisitID), ToVisitID: idPointer(leg.ToVisitID),
			DepartureAt: leg.DepartureAt, ArrivalAt: leg.ArrivalAt, Mode: string(leg.Mode),
			DistanceMeters: leg.DistanceMeters, Geometry: geometry, Verification: string(leg.Verification), Cost: cost(leg.Cost)})
	}
	for _, warning := range plan.Warnings {
		if warning.LegPosition != nil && !publicLegs[*warning.LegPosition] {
			continue
		}
		if warning.VisitID != nil && !visits[*warning.VisitID] {
			return SharedRoute{}, errInvalidProjection
		}
		item := w.Warning{Code: warning.Code, Scope: string(warning.Scope), Message: warning.Message,
			VisitID: idPointer(warning.VisitID)}
		if warning.LegPosition != nil {
			value := int64(*warning.LegPosition)
			item.LegPosition = &value
		}
		out.Warnings = append(out.Warnings, item)
	}
	for _, issue := range issues {
		if issue.Validate() != nil || issue.RouteID != route.ID {
			return SharedRoute{}, errInvalidProjection
		}
		item := w.RouteIssue{IssueID: idText([16]byte(issue.ID)), Type: string(issue.Type), State: string(issue.State),
			Code: issue.Details.Code, Message: issue.Details.Message, VisitID: idPointer(issue.VisitID),
			CreatedAt: issue.CreatedAt, ResolvedAt: copyTime(issue.ResolvedAt)}
		if issue.CatalogRevision != nil {
			value := strconv.FormatInt(int64(*issue.CatalogRevision), 10)
			item.CatalogRevision = &value
		}
		out.Issues = append(out.Issues, item)
	}
	return out, nil
}

func projectStep(step d.RouteStep) w.RouteStep {
	out := w.RouteStep{VisitID: idText([16]byte(step.VisitID)), Kind: string(step.Kind), Position: int64(step.Position),
		ArrivalAt: step.ArrivalAt, VisitStartAt: step.VisitStartAt, VisitEndAt: step.VisitEndAt, DepartureAt: step.DepartureAt,
		MinDurationSeconds: step.MinDurationSeconds, Pinned: step.Pinned, Obligation: step.Obligation,
		Participation:      w.ParticipationSnapshot{Status: string(step.Participation.Status), Evidence: string(step.Participation.Evidence)},
		AppliedConstraints: make([]w.AppliedConstraint, 0, len(step.AppliedConstraints))}
	if step.Catalog != nil {
		value := step.Catalog
		out.Catalog = &w.CatalogSnapshot{PlaceID: idPointer(value.PlaceID), EventID: idPointer(value.EventID),
			SessionID: idPointer(value.SessionID), Title: value.Title, Category: optionalText(value.Category),
			InterestMask: fmt.Sprintf("0x%016x", value.InterestMask), Availability: string(value.Availability),
			RegistrationDetails: optionalText(value.RegistrationDetails), AgeRequirements: optionalText(value.AgeRequirements),
			SessionStartsAt: value.SessionStartsAt, SessionEndsAt: value.SessionEndsAt, SessionVersion: optionalText(value.SessionVersion),
			DataMode: string(value.DataMode), Provenance: provenance(value.Provenance)}
	}
	if step.Cost != nil {
		value := cost(*step.Cost)
		out.Cost = &value
	}
	if step.Lunch != nil {
		out.Lunch = &w.LunchMetadata{AfterVisitID: idText([16]byte(step.Lunch.AfterVisitID)), DurationSeconds: step.Lunch.DurationSeconds}
	}
	if step.ExternalVenue != nil {
		venue := step.ExternalVenue
		out.ExternalVenue = &w.ExternalLunchVenue{Provider: venue.Provider, ExternalID: venue.ExternalID, Title: venue.Title,
			Address: venue.Address, Position: w.Coordinate{Latitude: venue.Position.Latitude, Longitude: venue.Position.Longitude},
			ObservedAt: venue.ObservedAt, Price: w.Price{Status: string(venue.Price.Status), Currency: venue.Price.Currency},
			Availability: string(venue.Availability), HoursVerification: string(venue.HoursVerification)}
	}
	for _, constraint := range step.AppliedConstraints {
		out.AppliedConstraints = append(out.AppliedConstraints, w.AppliedConstraint{Code: constraint.Code,
			Strength: string(constraint.Strength), Outcome: string(constraint.Outcome), Message: constraint.Message})
	}
	return out
}

func cost(value d.CostSnapshot) w.CostSnapshot {
	return w.CostSnapshot{PriceOfferID: idPointer(value.PriceOfferID), Audience: optionalText(value.Audience),
		Price: w.Price{Status: string(value.Price.Status), Currency: value.Price.Currency,
			LowerMinor: minor(value.Price.LowerMinor), UpperMinor: minor(value.Price.UpperMinor)},
		PersonalAmount: optionalMoney(value.PersonalAmount), ProgramAmount: optionalMoney(value.ProgramAmount),
		UnknownComponents: unknowns(value.UnknownComponents), Provenance: provenance(value.Provenance)}
}

func summary(value d.CostSummary) w.CostSummary {
	return w.CostSummary{KnownPersonal: money(value.KnownPersonal), KnownTransport: money(value.KnownTransport),
		ProgramAmount: money(value.ProgramAmount), TotalLower: optionalMoney(value.TotalLower), TotalUpper: optionalMoney(value.TotalUpper),
		UnknownComponents: unknowns(value.UnknownComponents), BudgetConclusion: string(value.BudgetConclusion)}
}

func provenance(value d.FactProvenance) w.PublicProvenance {
	return w.PublicProvenance{SourceName: value.SourceName, SourceUrl: value.SourceURL, SourceUpdatedAt: value.SourceUpdatedAt,
		FetchedAt: value.FetchedAt, VerifiedAt: value.VerifiedAt}
}

func money(value d.Money) w.Money {
	return w.Money{AmountMinor: strconv.FormatInt(value.AmountMinor, 10), Currency: value.Currency}
}

func optionalMoney(value *d.Money) *w.Money {
	if value == nil {
		return nil
	}
	out := money(*value)
	return &out
}

func minor(value *int64) *string {
	if value == nil {
		return nil
	}
	out := strconv.FormatInt(*value, 10)
	return &out
}

func unknowns(values []d.UnknownCostComponent) []w.UnknownCostComponent {
	out := make([]w.UnknownCostComponent, 0, len(values))
	for _, value := range values {
		out = append(out, w.UnknownCostComponent{Code: value.Code, Message: value.Message})
	}
	return out
}

func optionalText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func idPointer[T ~[16]byte](value *T) *string {
	if value == nil {
		return nil
	}
	out := idText([16]byte(*value))
	return &out
}

func idText(value [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[:4], value[4:6], value[6:8], value[8:10], value[10:])
}

func copyTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}
