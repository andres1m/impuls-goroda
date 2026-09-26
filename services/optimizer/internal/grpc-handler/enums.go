package grpchandler

import (
	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// enumMap pairs every wire value with its domain value; the UNSPECIFIED wire value is
// deliberately absent so that it is rejected instead of silently read as a default.
type enumMap[P comparable, D comparable] struct {
	toDomain map[P]D
	toProto  map[D]P
}

func newEnumMap[P comparable, D comparable](pairs map[P]D) enumMap[P, D] {
	reverse := make(map[D]P, len(pairs))
	for p, d := range pairs {
		reverse[d] = p
	}
	return enumMap[P, D]{toDomain: pairs, toProto: reverse}
}

var (
	priceStatuses = newEnumMap(map[pb.PriceStatus]domain.PriceStatus{
		pb.PriceStatus_PRICE_STATUS_FREE:    domain.PriceFree,
		pb.PriceStatus_PRICE_STATUS_FIXED:   domain.PriceFixed,
		pb.PriceStatus_PRICE_STATUS_RANGE:   domain.PriceRange,
		pb.PriceStatus_PRICE_STATUS_UNKNOWN: domain.PriceUnknown,
	})
	dataModes = newEnumMap(map[pb.DataMode]domain.DataMode{
		pb.DataMode_DATA_MODE_LIVE:      domain.DataLive,
		pb.DataMode_DATA_MODE_PREPARED:  domain.DataPrepared,
		pb.DataMode_DATA_MODE_SYNTHETIC: domain.DataSynthetic,
	})
	categories = newEnumMap(map[pb.Category]domain.Category{
		pb.Category_CATEGORY_CULTURE:   domain.CategoryCulture,
		pb.Category_CATEGORY_SPORT:     domain.CategorySport,
		pb.Category_CATEGORY_VOLUNTEER: domain.CategoryVolunteer,
		pb.Category_CATEGORY_WALK:      domain.CategoryWalk,
		pb.Category_CATEGORY_TOURISM:   domain.CategoryTourism,
		pb.Category_CATEGORY_GASTRO:    domain.CategoryGastro,
	})
	archetypes = newEnumMap(map[pb.Archetype]domain.Archetype{
		pb.Archetype_ARCHETYPE_URBAN_AVANTGARDE: domain.ArchetypeUrbanAvantgarde,
		pb.Archetype_ARCHETYPE_HISTORY_HERITAGE: domain.ArchetypeHistoryHeritage,
		pb.Archetype_ARCHETYPE_ACTION_SOCIAL:    domain.ArchetypeActionSocial,
	})
	participationStatuses = newEnumMap(map[pb.ParticipationStatus]domain.ParticipationStatus{
		pb.ParticipationStatus_PARTICIPATION_STATUS_NOT_REQUIRED:            domain.ParticipationNotRequired,
		pb.ParticipationStatus_PARTICIPATION_STATUS_ACTION_REQUIRED:         domain.ParticipationActionRequired,
		pb.ParticipationStatus_PARTICIPATION_STATUS_USER_REPORTED_CONFIRMED: domain.ParticipationUserReported,
		pb.ParticipationStatus_PARTICIPATION_STATUS_PROVIDER_CONFIRMED:      domain.ParticipationProviderConfirmed,
		pb.ParticipationStatus_PARTICIPATION_STATUS_UNAVAILABLE:             domain.ParticipationUnavailable,
	})
	verificationStatuses = newEnumMap(map[pb.VerificationStatus]domain.VerificationStatus{
		pb.VerificationStatus_VERIFICATION_STATUS_VERIFIED:    domain.VerificationVerified,
		pb.VerificationStatus_VERIFICATION_STATUS_ESTIMATED:   domain.VerificationEstimated,
		pb.VerificationStatus_VERIFICATION_STATUS_UNKNOWN:     domain.VerificationUnknown,
		pb.VerificationStatus_VERIFICATION_STATUS_UNAVAILABLE: domain.VerificationUnavailable,
	})
	budgetModes = newEnumMap(map[pb.BudgetMode]domain.BudgetMode{
		pb.BudgetMode_BUDGET_MODE_NONE:     domain.BudgetNone,
		pb.BudgetMode_BUDGET_MODE_ADVISORY: domain.BudgetAdvisory,
		pb.BudgetMode_BUDGET_MODE_STRICT:   domain.BudgetStrict,
	})
	resultStatuses = newEnumMap(map[pb.ResultStatus]domain.ResultStatus{
		pb.ResultStatus_RESULT_STATUS_READY:             domain.ResultReady,
		pb.ResultStatus_RESULT_STATUS_PARTIAL:           domain.ResultPartial,
		pb.ResultStatus_RESULT_STATUS_NO_FEASIBLE_ROUTE: domain.ResultNoFeasibleRoute,
		pb.ResultStatus_RESULT_STATUS_CONFLICT:          domain.ResultConflict,
	})
	availabilities = newEnumMap(map[pb.AvailabilityStatus]domain.Availability{
		pb.AvailabilityStatus_AVAILABILITY_STATUS_AVAILABLE:             domain.AvailabilityAvailable,
		pb.AvailabilityStatus_AVAILABILITY_STATUS_REGISTRATION_REQUIRED: domain.AvailabilityRegistrationRequired,
		pb.AvailabilityStatus_AVAILABILITY_STATUS_SOLD_OUT:              domain.AvailabilitySoldOut,
		pb.AvailabilityStatus_AVAILABILITY_STATUS_CANCELLED:             domain.AvailabilityCancelled,
		pb.AvailabilityStatus_AVAILABILITY_STATUS_UNKNOWN:               domain.AvailabilityUnknown,
	})
	budgetConclusions = newEnumMap(map[pb.BudgetConclusion]domain.BudgetConclusion{
		pb.BudgetConclusion_BUDGET_CONCLUSION_NOT_APPLICABLE: domain.BudgetNotApplicable,
		pb.BudgetConclusion_BUDGET_CONCLUSION_SATISFIED:      domain.BudgetSatisfied,
		pb.BudgetConclusion_BUDGET_CONCLUSION_VIOLATED:       domain.BudgetViolated,
		pb.BudgetConclusion_BUDGET_CONCLUSION_UNKNOWN:        domain.BudgetUnknown,
	})
	participationEvidences = newEnumMap(map[pb.ParticipationEvidence]domain.ParticipationEvidence{
		pb.ParticipationEvidence_PARTICIPATION_EVIDENCE_NONE:     domain.EvidenceNone,
		pb.ParticipationEvidence_PARTICIPATION_EVIDENCE_USER:     domain.EvidenceUser,
		pb.ParticipationEvidence_PARTICIPATION_EVIDENCE_PROVIDER: domain.EvidenceProvider,
	})
	constraintStrengths = newEnumMap(map[pb.ConstraintStrength]domain.ConstraintStrength{
		pb.ConstraintStrength_CONSTRAINT_STRENGTH_HARD: domain.StrengthHard,
		pb.ConstraintStrength_CONSTRAINT_STRENGTH_SOFT: domain.StrengthSoft,
	})
	constraintOutcomes = newEnumMap(map[pb.ConstraintOutcome]domain.ConstraintOutcome{
		pb.ConstraintOutcome_CONSTRAINT_OUTCOME_SATISFIED:   domain.OutcomeSatisfied,
		pb.ConstraintOutcome_CONSTRAINT_OUTCOME_CONDITIONAL: domain.OutcomeConditional,
	})
	stepKinds = newEnumMap(map[pb.VisitKind]domain.StepKind{
		pb.VisitKind_VISIT_KIND_VISIT:     domain.StepVisit,
		pb.VisitKind_VISIT_KIND_FREE_TIME: domain.StepFreeTime,
	})
	legEndpoints = newEnumMap(map[pb.LegEndpointKind]domain.LegEndpoint{
		pb.LegEndpointKind_LEG_ENDPOINT_KIND_ORIGIN:      domain.EndpointOrigin,
		pb.LegEndpointKind_LEG_ENDPOINT_KIND_VISIT:       domain.EndpointVisit,
		pb.LegEndpointKind_LEG_ENDPOINT_KIND_DESTINATION: domain.EndpointDestination,
	})
	scopes = newEnumMap(map[pb.TargetScope]domain.Scope{
		pb.TargetScope_TARGET_SCOPE_ROUTE: domain.ScopeRoute,
		pb.TargetScope_TARGET_SCOPE_VISIT: domain.ScopeVisit,
		pb.TargetScope_TARGET_SCOPE_LEG:   domain.ScopeLeg,
	})
	executionStatuses = newEnumMap(map[pb.ExecutionStatus]domain.ExecutionStatus{
		pb.ExecutionStatus_EXECUTION_STATUS_COMPLETED: domain.ExecutionCompleted,
		pb.ExecutionStatus_EXECUTION_STATUS_SKIPPED:   domain.ExecutionSkipped,
	})
	delayModes = newEnumMap(map[pb.DelayMode]domain.DelayMode{
		pb.DelayMode_DELAY_MODE_ALREADY_DELAYED: domain.DelayAlreadyDelayed,
		pb.DelayMode_DELAY_MODE_FUTURE_WAIT:     domain.DelayFutureWait,
	})
	positionSources = newEnumMap(map[pb.PositionSource]domain.PositionSource{
		pb.PositionSource_POSITION_SOURCE_DEVICE: domain.PositionDevice,
		pb.PositionSource_POSITION_SOURCE_MANUAL: domain.PositionManual,
	})
	removalModes = newEnumMap(map[pb.RemovalMode]domain.RemovalMode{
		pb.RemovalMode_REMOVAL_MODE_REBUILD:   domain.RemovalRebuild,
		pb.RemovalMode_REMOVAL_MODE_FREE_TIME: domain.RemovalFreeTime,
	})
	pinKinds = newEnumMap(map[pb.PinKind]domain.PinKind{
		pb.PinKind_PIN_KIND_PREFERRED:  domain.PinPreferred,
		pb.PinKind_PIN_KIND_OBLIGATION: domain.PinObligation,
		pb.PinKind_PIN_KIND_NONE:       domain.PinNone,
	})
	recomputeStatuses = newEnumMap(map[pb.RecomputeStatus]domain.RecomputeStatus{
		pb.RecomputeStatus_RECOMPUTE_STATUS_PROPOSED:  domain.RecomputeProposed,
		pb.RecomputeStatus_RECOMPUTE_STATUS_UNCHANGED: domain.RecomputeUnchanged,
		pb.RecomputeStatus_RECOMPUTE_STATUS_CONFLICT:  domain.RecomputeConflict,
	})
	changeKinds = newEnumMap(map[pb.RouteChangeKind]domain.ChangeKind{
		pb.RouteChangeKind_ROUTE_CHANGE_KIND_KEPT:                 domain.ChangeKept,
		pb.RouteChangeKind_ROUTE_CHANGE_KIND_REMOVED:              domain.ChangeRemoved,
		pb.RouteChangeKind_ROUTE_CHANGE_KIND_REPLACED:             domain.ChangeReplaced,
		pb.RouteChangeKind_ROUTE_CHANGE_KIND_TIME_SHIFTED:         domain.ChangeTimeShifted,
		pb.RouteChangeKind_ROUTE_CHANGE_KIND_COST_CHANGED:         domain.ChangeCostChanged,
		pb.RouteChangeKind_ROUTE_CHANGE_KIND_PARTICIPATION_ACTION: domain.ChangeParticipationAction,
		pb.RouteChangeKind_ROUTE_CHANGE_KIND_VERIFICATION_CHANGED: domain.ChangeVerificationChanged,
	})
)
