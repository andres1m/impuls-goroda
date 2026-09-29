package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
)

const (
	fieldStatus               = "status"
	fieldRouteID              = "route_id"
	fieldVisitID              = "visit_id"
	fieldRevision             = "revision"
	statusUnchanged           = "UNCHANGED"
	statusReady               = "READY"
	actionExternalLinkOpened  = "external_link_opened"
	actionConfirmed           = "user_reported_confirmed"
	actionUnavailable         = "user_reported_unavailable"
	actionClearReport         = "clear_user_report"
	maxPrivateReferenceLength = 2048
)

type VisitCommand struct {
	ActorID          domain.UserID
	RouteID          domain.RouteID
	VisitID          domain.VisitID
	ExpectedRevision domain.RouteRevisionNumber
	Key              [16]byte
}

type ParticipationInput struct {
	Action           string  `json:"action"`
	PrivateReference *string `json:"private_reference,omitempty"`
}

type ExecutionInput struct {
	Status           domain.ExecutionStatus  `json:"status"`
	ActualStartedAt  *time.Time              `json:"actual_started_at,omitempty"`
	ActualEndedAt    *time.Time              `json:"actual_ended_at,omitempty"`
	ConfirmationKind domain.ConfirmationKind `json:"confirmation_kind"`
}

func (r *Runtime) UpdateParticipation(
	ctx context.Context,
	target VisitCommand,
	input ParticipationInput,
) (command.Result, error) {
	if r.commands == nil {
		return command.Result{}, errors.New("gateway commands are not initialized")
	}
	if err := validateParticipationInput(input); err != nil {
		return command.Result{}, err
	}
	envelope, err := visitEnvelope(target, command.UpdateVisitParticipation, input)
	if err != nil {
		return command.Result{}, err
	}
	return r.commands.Execute(ctx, &envelope, func(q *postgres.Queries) (command.Result, error) {
		return r.mutateParticipation(ctx, q, target, input)
	})
}

func (r *Runtime) mutateParticipation(
	ctx context.Context,
	q *postgres.Queries,
	target VisitCommand,
	input ParticipationInput,
) (command.Result, error) {
	access, err := q.LockOwnedRoute(ctx, target.RouteID, target.ActorID)
	if err != nil {
		return command.Result{}, err
	}
	if revErr := postgres.RequireRevision(access, target.ExpectedRevision); revErr != nil {
		return command.Result{}, revErr
	}
	current, err := q.Participation(ctx, target.RouteID, target.VisitID, access.Revision)
	if err != nil {
		return command.Result{}, err
	}
	now := r.clock().UTC()
	updated, revision, status, err := applyParticipationAction(
		ctx,
		q,
		target,
		&current,
		input,
		access.Revision,
		now,
	)
	if err != nil {
		return command.Result{}, err
	}
	body, err := json.Marshal(map[string]any{
		fieldStatus:     status,
		fieldRouteID:    idString(target.RouteID[:]),
		fieldRevision:   strconv.FormatInt(int64(revision), 10),
		"participation": participationResponse(&updated),
	})
	if err != nil {
		return command.Result{}, fmt.Errorf("marshal participation response: %w", err)
	}
	return command.Result{
		HTTPStatus:        http.StatusOK,
		ResponseBody:      body,
		RouteID:           &target.RouteID,
		ResultingRevision: &revision,
	}, nil
}

func applyParticipationAction(
	ctx context.Context,
	q *postgres.Queries,
	target VisitCommand,
	current *domain.Participation,
	input ParticipationInput,
	revision domain.RouteRevisionNumber,
	now time.Time,
) (domain.Participation, domain.RouteRevisionNumber, string, error) {
	if input.Action == actionExternalLinkOpened {
		opened, err := q.RecordLinkOpened(ctx, current, now)
		if err != nil {
			return domain.Participation{}, 0, "", err
		}
		return opened, revision, statusUnchanged, nil
	}
	if current.Evidence == domain.EvidenceProvider {
		return *current, revision, statusUnchanged, nil
	}
	next, err := nextUserParticipation(ctx, q, target, current, input)
	if err != nil {
		return domain.Participation{}, 0, "", err
	}
	if next.Status == current.Status && next.Evidence == current.Evidence &&
		sameString(next.PrivateReference, current.PrivateReference) {
		return *current, revision, statusUnchanged, nil
	}
	nextRevision := revision + 1
	next.UpdatedInRevision, next.UpdatedAt = nextRevision, now
	snapshot := domain.ParticipationSnapshot{Status: next.Status, Evidence: next.Evidence}
	if _, err := q.CloneVisitRevision(
		ctx,
		target.RouteID,
		revision,
		target.VisitID,
		domain.MutationParticipation,
		&snapshot,
		now,
	); err != nil {
		return domain.Participation{}, 0, "", err
	}
	if err := q.UpdateParticipation(ctx, current, &next, nextRevision); err != nil {
		return domain.Participation{}, 0, "", err
	}
	return next, nextRevision, statusReady, nil
}

func nextUserParticipation(
	ctx context.Context,
	q *postgres.Queries,
	target VisitCommand,
	current *domain.Participation,
	input ParticipationInput,
) (domain.Participation, error) {
	next := *current
	switch input.Action {
	case actionConfirmed:
		next.Status = domain.ParticipationUserReported
		next.Evidence = domain.EvidenceUser
		next.PrivateReference = input.PrivateReference
	case actionUnavailable:
		next.Status = domain.ParticipationUnavailable
		next.Evidence = domain.EvidenceUser
		next.PrivateReference = nil
	case actionClearReport:
		if current.Evidence == domain.EvidenceUser {
			initialStatus, err := q.InitialParticipation(ctx, target.RouteID, target.VisitID)
			if err != nil {
				return domain.Participation{}, err
			}
			next.Status = initialStatus
			next.Evidence = domain.EvidenceNone
			next.PrivateReference = nil
		}
	}
	return next, nil
}

func (r *Runtime) UpdateExecution(
	ctx context.Context,
	target VisitCommand,
	input ExecutionInput,
) (command.Result, error) {
	if r.commands == nil {
		return command.Result{}, errors.New("gateway commands are not initialized")
	}
	if err := validateExecutionInput(input); err != nil {
		return command.Result{}, err
	}
	envelope, err := visitEnvelope(target, command.UpdateVisitExecution, input)
	if err != nil {
		return command.Result{}, err
	}
	return r.commands.Execute(ctx, &envelope, func(q *postgres.Queries) (command.Result, error) {
		return r.mutateExecution(ctx, q, target, input)
	})
}

func (r *Runtime) mutateExecution(
	ctx context.Context,
	q *postgres.Queries,
	target VisitCommand,
	input ExecutionInput,
) (command.Result, error) {
	access, err := q.LockOwnedRoute(ctx, target.RouteID, target.ActorID)
	if err != nil {
		return command.Result{}, err
	}
	if revErr := postgres.RequireRevision(access, target.ExpectedRevision); revErr != nil {
		return command.Result{}, revErr
	}
	current, err := q.Execution(ctx, target.RouteID, target.VisitID, access.Revision)
	if err != nil {
		return command.Result{}, err
	}
	updated, revision, status, err := applyExecutionInput(
		ctx,
		q,
		target,
		&current,
		input,
		access.Revision,
		r.clock().UTC(),
	)
	if err != nil {
		return command.Result{}, err
	}
	body, err := json.Marshal(map[string]any{
		fieldStatus:   status,
		fieldRouteID:  idString(target.RouteID[:]),
		fieldRevision: strconv.FormatInt(int64(revision), 10),
		"execution":   executionResponse(&updated),
	})
	if err != nil {
		return command.Result{}, fmt.Errorf("marshal execution response: %w", err)
	}
	return command.Result{
		HTTPStatus:        http.StatusOK,
		ResponseBody:      body,
		RouteID:           &target.RouteID,
		ResultingRevision: &revision,
	}, nil
}

func applyExecutionInput(
	ctx context.Context,
	q *postgres.Queries,
	target VisitCommand,
	current *domain.Execution,
	input ExecutionInput,
	revision domain.RouteRevisionNumber,
	now time.Time,
) (domain.Execution, domain.RouteRevisionNumber, string, error) {
	if current.Confirmation == domain.ConfirmationProviderConfirmed {
		return *current, revision, statusUnchanged, nil
	}
	if current.Status == input.Status && sameTime(current.ActualStartedAt, input.ActualStartedAt) &&
		sameTime(current.ActualEndedAt, input.ActualEndedAt) {
		return *current, revision, statusUnchanged, nil
	}
	next := *current
	nextRevision := revision + 1
	next.Status = input.Status
	next.ActualStartedAt = input.ActualStartedAt
	next.ActualEndedAt = input.ActualEndedAt
	next.Confirmation = domain.ConfirmationUserReported
	next.UpdatedInRevision = nextRevision
	next.UpdatedAt = now
	if _, err := q.CloneVisitRevision(
		ctx,
		target.RouteID,
		revision,
		target.VisitID,
		domain.MutationExecution,
		nil,
		now,
	); err != nil {
		return domain.Execution{}, 0, "", err
	}
	if err := q.UpdateExecution(ctx, &next, nextRevision); err != nil {
		return domain.Execution{}, 0, "", err
	}
	return next, nextRevision, statusReady, nil
}

func visitEnvelope(target VisitCommand, operation command.Operation, body any) (command.Envelope, error) {
	hash, err := command.Fingerprint(&command.FingerprintInput{
		ActorID:   target.ActorID,
		Operation: operation,
		Path: []command.PathComponent{
			{Name: fieldRouteID, Value: idString(target.RouteID[:])},
			{Name: fieldVisitID, Value: idString(target.VisitID[:])},
		},
		ExpectedRevision: &target.ExpectedRevision,
		Body:             body,
	})
	if err != nil {
		return command.Envelope{}, err
	}
	return command.Envelope{ActorID: target.ActorID, Operation: operation, Key: target.Key, RequestHash: hash}, nil
}

func validateParticipationInput(input ParticipationInput) error {
	switch input.Action {
	case actionExternalLinkOpened, actionUnavailable, actionClearReport:
		if input.PrivateReference != nil {
			return postgres.ErrInvalidVisitAction
		}
	case actionConfirmed:
		if input.PrivateReference != nil {
			runes := utf8.RuneCountInString(*input.PrivateReference)
			if runes == 0 || runes > maxPrivateReferenceLength {
				return postgres.ErrInvalidVisitAction
			}
		}
	default:
		return postgres.ErrInvalidVisitAction
	}
	return nil
}

func validateExecutionInput(input ExecutionInput) error {
	if input.ConfirmationKind != domain.ConfirmationUserReported ||
		(input.Status != domain.ExecutionCompleted && input.Status != domain.ExecutionSkipped) {
		return postgres.ErrInvalidVisitAction
	}
	if input.ActualStartedAt != nil && input.ActualEndedAt != nil &&
		input.ActualEndedAt.Before(*input.ActualStartedAt) {
		return postgres.ErrInvalidVisitAction
	}
	return nil
}

func participationResponse(item *domain.Participation) map[string]any {
	result := map[string]any{
		fieldVisitID:          idString(item.VisitID[:]),
		fieldStatus:           item.Status,
		"evidence":            item.Evidence,
		"updated_in_revision": strconv.FormatInt(int64(item.UpdatedInRevision), 10),
		"updated_at":          item.UpdatedAt,
	}
	if item.ExternalLinkOpenedAt != nil {
		result["external_link_opened_at"] = item.ExternalLinkOpenedAt
	}
	return result
}

func executionResponse(item *domain.Execution) map[string]any {
	result := map[string]any{
		fieldVisitID:          idString(item.VisitID[:]),
		fieldStatus:           item.Status,
		"confirmation_kind":   item.Confirmation,
		"updated_in_revision": strconv.FormatInt(int64(item.UpdatedInRevision), 10),
		"updated_at":          item.UpdatedAt,
	}
	if item.ActualStartedAt != nil {
		result["actual_started_at"] = item.ActualStartedAt
	}
	if item.ActualEndedAt != nil {
		result["actual_ended_at"] = item.ActualEndedAt
	}
	return result
}

func sameString(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func sameTime(a, b *time.Time) bool {
	return a == nil && b == nil || a != nil && b != nil && a.Equal(*b)
}

func idString(id []byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
}
