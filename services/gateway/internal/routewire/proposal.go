package routewire

import (
	"math"
	"strconv"
	"time"
	"unicode/utf8"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
)

type ProposalChange struct {
	Kind               string                `json:"kind"`
	Scope              d.WarningScope        `json:"scope"`
	BeforeVisitID      *string               `json:"before_visit_id,omitempty"`
	AfterVisitID       *string               `json:"after_visit_id,omitempty"`
	LegPosition        *int                  `json:"leg_position,omitempty"`
	Message            string                `json:"message"`
	TimeShiftSeconds   *int64                `json:"time_shift_seconds,omitempty"`
	BeforeCost         *Money                `json:"before_cost,omitempty"`
	AfterCost          *Money                `json:"after_cost,omitempty"`
	Action             *string               `json:"action,omitempty"`
	BeforeVerification *d.VerificationStatus `json:"before_verification,omitempty"`
	AfterVerification  *d.VerificationStatus `json:"after_verification,omitempty"`
}

type PendingProposal struct {
	ProposalID          string           `json:"proposal_id"`
	BaseRevision        string           `json:"base_revision"`
	BaseCatalogRevision string           `json:"base_catalog_revision"`
	Reason              string           `json:"reason"`
	State               string           `json:"state"`
	Candidate           RoutePlan        `json:"candidate"`
	Changes             []ProposalChange `json:"changes"`
	Conflicts           []Conflict       `json:"conflicts"`
	CreatedAt           time.Time        `json:"created_at"`
	EffectiveStartAt    *time.Time       `json:"effective_start_at,omitempty"`
}

func ProposalChangesToWire(changes []RecomputedChange) ([]ProposalChange, error) {
	out := make([]ProposalChange, 0, len(changes))
	for _, change := range changes {
		if change.TimeShiftSeconds != nil && (*change.TimeShiftSeconds < math.MinInt32 || *change.TimeShiftSeconds > math.MaxInt32) {
			return nil, ErrInvalidResult
		}
		if change.ParticipationAction != nil && (!utf8.ValidString(*change.ParticipationAction) || utf8.RuneCountInString(*change.ParticipationAction) > 128) {
			return nil, ErrInvalidResult
		}
		out = append(out, ProposalChange{Kind: change.Kind, Scope: change.Scope, BeforeVisitID: idWire(change.BeforeVisitID), AfterVisitID: idWire(change.AfterVisitID), LegPosition: change.LegPosition, Message: change.Message, TimeShiftSeconds: change.TimeShiftSeconds, BeforeCost: optionalMoneyWire(change.CostBefore), AfterCost: optionalMoneyWire(change.CostAfter), Action: change.ParticipationAction, BeforeVerification: change.VerificationBefore, AfterVerification: change.VerificationAfter})
	}
	return out, nil
}

func PendingRemovalToWire(id d.ProposalID, revision d.RouteRevisionNumber, catalog d.CatalogRevision, candidate d.RoutePlanSnapshot, changes []ProposalChange, now time.Time) (PendingProposal, error) {
	return pendingProposalToWire(id, revision, catalog, candidate, changes, now, "delete")
}

func PendingPanicToWire(id d.ProposalID, revision d.RouteRevisionNumber, catalog d.CatalogRevision, candidate d.RoutePlanSnapshot, changes []ProposalChange, now, effective time.Time) (PendingProposal, error) {
	if effective.IsZero() {
		return PendingProposal{}, ErrInvalidResult
	}
	proposal, err := pendingProposalToWire(id, revision, catalog, candidate, changes, now, "delay")
	if err != nil {
		return PendingProposal{}, err
	}
	proposal.EffectiveStartAt = &effective
	return proposal, nil
}

func pendingProposalToWire(id d.ProposalID, revision d.RouteRevisionNumber, catalog d.CatalogRevision, candidate d.RoutePlanSnapshot, changes []ProposalChange, now time.Time, reason string) (PendingProposal, error) {
	if id == (d.ProposalID{}) || revision <= 0 || catalog <= 0 || now.IsZero() {
		return PendingProposal{}, ErrInvalidResult
	}
	plan, err := PlanToWire(candidate)
	if err != nil {
		return PendingProposal{}, err
	}
	return PendingProposal{ProposalID: *idWire(&id), BaseRevision: strconv.FormatInt(int64(revision), 10), BaseCatalogRevision: strconv.FormatInt(int64(catalog), 10), Reason: reason, State: "pending", Candidate: plan, Changes: changes, Conflicts: []Conflict{}, CreatedAt: now}, nil
}
