package domain

import (
	"math"
	"testing"
	"time"
)

func int64Pointer(value int64) *int64 { return &value }

func validProvenance() FactProvenance {
	return FactProvenance{SourceName: "catalog", FetchedAt: instant}
}

func validCostSnapshot() CostSnapshot {
	zero := int64(0)
	personal := Money{AmountMinor: 0, Currency: "RUB"}
	program := Money{AmountMinor: 0, Currency: "RUB"}
	return CostSnapshot{
		Price:          Price{Status: PriceFree, Currency: "RUB", LowerMinor: &zero, UpperMinor: &zero},
		PersonalAmount: &personal,
		ProgramAmount:  &program,
		Provenance:     validProvenance(),
	}
}

func validCostSummary() CostSummary {
	return CostSummary{
		KnownPersonal:    Money{Currency: "RUB"},
		KnownTransport:   Money{Currency: "RUB"},
		ProgramAmount:    Money{Currency: "RUB"},
		BudgetConclusion: BudgetNotApplicable,
	}
}

func validConstraints() RouteConstraints {
	return RouteConstraints{
		ExcludedCategories: []string{"sport"},
		MovementModes:      []MovementMode{"walk"},
		LoadProfile:        "standard",
		Budget:             Budget{Mode: BudgetNone},
	}
}

func validPlan() RoutePlanSnapshot {
	visitID := VisitID(id())
	placeID := PlaceID(id())
	cost := validCostSnapshot()
	return RoutePlanSnapshot{
		SchemaVersion:   1,
		Lifecycle:       RouteDraft,
		ArchetypeID:     "history_heritage",
		Timezone:        "Europe/Moscow",
		StartAt:         instant,
		EndAt:           instant.Add(4 * time.Hour),
		Origin:          Coordinate{Longitude: 37.6176, Latitude: 55.7558},
		Constraints:     validConstraints(),
		CatalogRevision: 1,
		Result:          ResultReady,
		Warnings:        []Warning{{Code: "CHECK_ACCESS", Scope: WarningRoute, Message: "Проверьте условия входа"}},
		Conflicts:       []Conflict{{Code: "NONE", VisitIDs: []VisitID{visitID}, Message: "Нет активного конфликта"}},
		Cost:            validCostSummary(),
		Geometry:        []Coordinate{{Longitude: 37.6176, Latitude: 55.7558}},
		Steps: []RouteStep{{
			VisitID:            visitID,
			Kind:               VisitPlace,
			Position:           1,
			ArrivalAt:          instant.Add(30 * time.Minute),
			VisitStartAt:       instant.Add(30 * time.Minute),
			VisitEndAt:         instant.Add(90 * time.Minute),
			DepartureAt:        instant.Add(90 * time.Minute),
			MinDurationSeconds: 3600,
			Participation:      ParticipationSnapshot{Status: ParticipationNotRequired, Evidence: EvidenceNone},
			Catalog: &CatalogSnapshot{
				PlaceID:      &placeID,
				Title:        "Museum",
				Category:     "culture",
				Availability: AvailabilityAvailable,
				DataMode:     DataLive,
				Provenance:   validProvenance(),
			},
			Cost: &cost,
		}},
		Legs: []RouteLeg{{
			Position:     1,
			FromKind:     LegOrigin,
			ToKind:       LegVisit,
			ToVisitID:    &visitID,
			DepartureAt:  instant,
			ArrivalAt:    instant.Add(30 * time.Minute),
			Mode:         "walk",
			Geometry:     []Coordinate{{Longitude: 37.6176, Latitude: 55.7558}},
			Verification: VerificationEstimated,
			Evidence:     LegEvidence{Provider: "estimate", Method: "distance", ObservedAt: instant, Mode: "walk"},
			Cost:         validCostSnapshot(),
		}},
	}
}

func TestPriceRepresentsZeroAndUnknownSeparately(t *testing.T) {
	zero := int64(0)
	free := Price{Status: PriceFree, Currency: "RUB", LowerMinor: &zero, UpperMinor: &zero}
	if err := free.Validate(); err != nil {
		t.Fatal(err)
	}
	fixedZero := Price{Status: PriceFixed, Currency: "RUB", LowerMinor: &zero, UpperMinor: &zero}
	if err := fixedZero.Validate(); err != nil {
		t.Fatal(err)
	}
	unknown := Price{Status: PriceUnknown, Currency: "RUB"}
	if err := unknown.Validate(); err != nil {
		t.Fatal(err)
	}
	unknown.LowerMinor = &zero
	if unknown.Validate() == nil {
		t.Fatal("unknown price with a numeric bound accepted")
	}
	rangePrice := Price{Status: PriceRange, Currency: "RUB", LowerMinor: int64Pointer(200), UpperMinor: int64Pointer(100)}
	if rangePrice.Validate() == nil {
		t.Fatal("reversed price range accepted")
	}
}

func TestRoutePlanValidatesScheduleAndUnknownBudget(t *testing.T) {
	plan := validPlan()
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	if plan.Legs[0].Verification != VerificationEstimated {
		t.Fatal("geometry changed transition verification")
	}

	duplicate := plan.Steps[0]
	duplicate.VisitID = VisitID([16]byte{2})
	plan.Steps = append(plan.Steps, duplicate)
	if plan.Validate() == nil {
		t.Fatal("duplicate step position accepted")
	}

	plan = validPlan()
	limit := Money{AmountMinor: 1000, Currency: "RUB"}
	plan.Constraints.Budget = Budget{Mode: BudgetStrict, Limit: &limit}
	plan.Cost.UnknownComponents = []UnknownCostComponent{{Code: "MEAL", Message: "Стоимость питания неизвестна"}}
	plan.Cost.BudgetConclusion = BudgetSatisfied
	if plan.Validate() == nil {
		t.Fatal("unknown strict budget reported as satisfied")
	}
	plan.Cost.BudgetConclusion = BudgetUnknown
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}

	plan = validPlan()
	plan.Legs[0].Cost.Price.Currency = "USD"
	if plan.Validate() == nil {
		t.Fatal("mixed route currencies accepted")
	}
}

func TestFreeTimeHasNoCatalogOrCostSnapshot(t *testing.T) {
	step := validPlan().Steps[0]
	step.Kind = VisitFreeTime
	step.MinDurationSeconds = 0
	if step.Validate() == nil {
		t.Fatal("free time with catalog data accepted")
	}
	step.Catalog = nil
	step.Cost = nil
	if err := step.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSpatialValuesRejectNonFiniteNumbers(t *testing.T) {
	coordinate := Coordinate{Longitude: math.NaN(), Latitude: 55}
	if coordinate.Validate() == nil {
		t.Fatal("NaN coordinate accepted")
	}
	leg := validPlan().Legs[0]
	distance := math.Inf(1)
	leg.DistanceMeters = &distance
	if leg.Validate() == nil {
		t.Fatal("infinite distance accepted")
	}
}

func TestRoutePlanCloneDoesNotShareMutableCollections(t *testing.T) {
	original := validPlan()
	clone := original.Clone()

	clone.Constraints.ExcludedCategories[0] = "culture"
	clone.Geometry[0].Longitude = 1
	clone.Steps[0].Catalog.Title = "Changed"
	clone.Legs[0].Evidence.Limitations = append(clone.Legs[0].Evidence.Limitations, "stairs")
	clone.Conflicts[0].VisitIDs[0] = VisitID([16]byte{2})

	if original.Constraints.ExcludedCategories[0] != "sport" || original.Geometry[0].Longitude == 1 || original.Steps[0].Catalog.Title == "Changed" {
		t.Fatal("snapshot clone shares mutable state")
	}
	if len(original.Legs[0].Evidence.Limitations) != 0 || original.Conflicts[0].VisitIDs[0] != VisitID(id()) {
		t.Fatal("nested snapshot collections were not isolated")
	}
}

func TestProposalAndIssueStateBoundaries(t *testing.T) {
	proposal := RouteProposal{
		ID:                  ProposalID(id()),
		RouteID:             RouteID(id()),
		BaseRevision:        1,
		BaseCatalogRevision: 1,
		Reason:              ProposalDelay,
		State:               ProposalPending,
		Candidate:           validPlan(),
		Changes: []RouteChange{{
			Kind:             ChangeTimeShifted,
			Scope:            ChangeVisitScope,
			BeforeVisitID:    func() *VisitID { value := VisitID(id()); return &value }(),
			TimeShiftSeconds: int64Pointer(600),
			Message:          "Посещение сдвинуто",
		}},
		CreatedAt: instant,
	}
	if err := proposal.Validate(); err != nil {
		t.Fatal(err)
	}
	clone := proposal.Clone()
	clone.Candidate.Geometry[0].Longitude = 1
	clone.Changes[0].Message = "Changed"
	if proposal.Candidate.Geometry[0].Longitude == 1 || proposal.Changes[0].Message == "Changed" {
		t.Fatal("proposal clone shares mutable state")
	}
	proposal.State = ProposalApplied
	if proposal.Validate() == nil {
		t.Fatal("applied proposal without resolution accepted")
	}
	resolved := instant.Add(time.Minute)
	appliedRevision := RouteRevisionNumber(2)
	proposal.ResolvedAt = &resolved
	proposal.AppliedRevision = &appliedRevision
	if err := proposal.Validate(); err != nil {
		t.Fatal(err)
	}

	issue := RouteIssue{
		ID:        IssueID(id()),
		RouteID:   RouteID(id()),
		Type:      IssueCancelled,
		Details:   IssueDetails{Code: "SESSION_CANCELLED", Message: "Сеанс отменён"},
		State:     IssueOpen,
		CreatedAt: instant,
	}
	if err := issue.Validate(); err != nil {
		t.Fatal(err)
	}
	issue.State = IssueResolved
	if issue.Validate() == nil {
		t.Fatal("resolved issue without resolution time accepted")
	}
}

func TestRouteRevisionRequiresOrderedParent(t *testing.T) {
	parent := RouteRevisionNumber(2)
	revision := RouteRevision{
		RouteID:   RouteID(id()),
		Number:    2,
		Parent:    &parent,
		Plan:      validPlan(),
		Mutation:  MutationApply,
		CreatedAt: instant,
	}
	if revision.Validate() == nil {
		t.Fatal("revision accepted a non-preceding parent")
	}
	parent = 1
	if err := revision.Validate(); err != nil {
		t.Fatal(err)
	}
}
