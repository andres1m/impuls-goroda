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

func (r *Runtime) UpdateParticipation(ctx context.Context, target VisitCommand, input ParticipationInput) (command.Result, error) {
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
	return r.commands.Execute(ctx, envelope, func(q *postgres.Queries) (command.Result, error) {
		access, err := q.LockOwnedRoute(ctx, target.RouteID, target.ActorID)
		if err != nil {
			return command.Result{}, err
		}
		if err := postgres.RequireRevision(access, target.ExpectedRevision); err != nil {
			return command.Result{}, err
		}
		current, err := q.Participation(ctx, target.RouteID, target.VisitID, access.Revision)
		if err != nil {
			return command.Result{}, err
		}
		now := r.clock().UTC()
		status := "UNCHANGED"
		revision := access.Revision
		if input.Action == "external_link_opened" {
			current, err = q.RecordLinkOpened(ctx, current, now)
			if err != nil {
				return command.Result{}, err
			}
		} else if current.Evidence != domain.EvidenceProvider {
			next := current
			switch input.Action {
			case "user_reported_confirmed":
				next.Status, next.Evidence, next.PrivateReference = domain.ParticipationUserReported, domain.EvidenceUser, input.PrivateReference
			case "user_reported_unavailable":
				next.Status, next.Evidence, next.PrivateReference = domain.ParticipationUnavailable, domain.EvidenceUser, nil
			case "clear_user_report":
				if current.Evidence == domain.EvidenceUser {
					next.Status, err = q.InitialParticipation(ctx, target.RouteID, target.VisitID)
					if err != nil {
						return command.Result{}, err
					}
					next.Evidence, next.PrivateReference = domain.EvidenceNone, nil
				}
			}
			if next.Status != current.Status || next.Evidence != current.Evidence || !sameString(next.PrivateReference, current.PrivateReference) {
				revision = access.Revision + 1
				next.UpdatedInRevision, next.UpdatedAt = revision, now
				snapshot := domain.ParticipationSnapshot{Status: next.Status, Evidence: next.Evidence}
				if _, err := q.CloneVisitRevision(ctx, target.RouteID, access.Revision, target.VisitID, domain.MutationParticipation, &snapshot, now); err != nil {
					return command.Result{}, err
				}
				if err := q.UpdateParticipation(ctx, current, next, revision); err != nil {
					return command.Result{}, err
				}
				current = next
				status = "READY"
			}
		}
		body, err := json.Marshal(map[string]any{
			"status": status, "route_id": idString(target.RouteID[:]), "revision": strconv.FormatInt(int64(revision), 10),
			"participation": participationResponse(current),
		})
		if err != nil {
			return command.Result{}, err
		}
		return command.Result{HTTPStatus: http.StatusOK, ResponseBody: body, RouteID: &target.RouteID, ResultingRevision: &revision}, nil
	})
}

func (r *Runtime) UpdateExecution(ctx context.Context, target VisitCommand, input ExecutionInput) (command.Result, error) {
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
	return r.commands.Execute(ctx, envelope, func(q *postgres.Queries) (command.Result, error) {
		access, err := q.LockOwnedRoute(ctx, target.RouteID, target.ActorID)
		if err != nil {
			return command.Result{}, err
		}
		if err := postgres.RequireRevision(access, target.ExpectedRevision); err != nil {
			return command.Result{}, err
		}
		current, err := q.Execution(ctx, target.RouteID, target.VisitID, access.Revision)
		if err != nil {
			return command.Result{}, err
		}
		status := "UNCHANGED"
		revision := access.Revision
		if current.Confirmation != domain.ConfirmationProviderConfirmed {
			if current.Status != input.Status || !sameTime(current.ActualStartedAt, input.ActualStartedAt) || !sameTime(current.ActualEndedAt, input.ActualEndedAt) {
				now := r.clock().UTC()
				revision = access.Revision + 1
				current.Status, current.ActualStartedAt, current.ActualEndedAt = input.Status, input.ActualStartedAt, input.ActualEndedAt
				current.Confirmation, current.UpdatedInRevision, current.UpdatedAt = domain.ConfirmationUserReported, revision, now
				if _, err := q.CloneVisitRevision(ctx, target.RouteID, access.Revision, target.VisitID, domain.MutationExecution, nil, now); err != nil {
					return command.Result{}, err
				}
				if err := q.UpdateExecution(ctx, current, revision); err != nil {
					return command.Result{}, err
				}
				status = "READY"
			}
		}
		body, err := json.Marshal(map[string]any{
			"status": status, "route_id": idString(target.RouteID[:]), "revision": strconv.FormatInt(int64(revision), 10),
			"execution": executionResponse(current),
		})
		if err != nil {
			return command.Result{}, err
		}
		return command.Result{HTTPStatus: http.StatusOK, ResponseBody: body, RouteID: &target.RouteID, ResultingRevision: &revision}, nil
	})
}

func visitEnvelope(target VisitCommand, operation command.Operation, body any) (command.Envelope, error) {
	hash, err := command.Fingerprint(command.FingerprintInput{
		ActorID: target.ActorID, Operation: operation,
		Path:             []command.PathComponent{{Name: "route_id", Value: idString(target.RouteID[:])}, {Name: "visit_id", Value: idString(target.VisitID[:])}},
		ExpectedRevision: &target.ExpectedRevision, Body: body,
	})
	if err != nil {
		return command.Envelope{}, err
	}
	return command.Envelope{ActorID: target.ActorID, Operation: operation, Key: target.Key, RequestHash: hash}, nil
}

func validateParticipationInput(input ParticipationInput) error {
	switch input.Action {
	case "external_link_opened", "user_reported_unavailable", "clear_user_report":
		if input.PrivateReference != nil {
			return postgres.ErrInvalidVisitAction
		}
	case "user_reported_confirmed":
		if input.PrivateReference != nil && (utf8.RuneCountInString(*input.PrivateReference) == 0 || utf8.RuneCountInString(*input.PrivateReference) > 2048) {
			return postgres.ErrInvalidVisitAction
		}
	default:
		return postgres.ErrInvalidVisitAction
	}
	return nil
}

func validateExecutionInput(input ExecutionInput) error {
	if input.ConfirmationKind != domain.ConfirmationUserReported || (input.Status != domain.ExecutionCompleted && input.Status != domain.ExecutionSkipped) {
		return postgres.ErrInvalidVisitAction
	}
	if input.ActualStartedAt != nil && input.ActualEndedAt != nil && input.ActualEndedAt.Before(*input.ActualStartedAt) {
		return postgres.ErrInvalidVisitAction
	}
	return nil
}

func participationResponse(item domain.Participation) map[string]any {
	result := map[string]any{
		"visit_id": idString(item.VisitID[:]), "status": item.Status, "evidence": item.Evidence,
		"updated_in_revision": strconv.FormatInt(int64(item.UpdatedInRevision), 10), "updated_at": item.UpdatedAt,
	}
	if item.ExternalLinkOpenedAt != nil {
		result["external_link_opened_at"] = item.ExternalLinkOpenedAt
	}
	return result
}

func executionResponse(item domain.Execution) map[string]any {
	result := map[string]any{
		"visit_id": idString(item.VisitID[:]), "status": item.Status, "confirmation_kind": item.Confirmation,
		"updated_in_revision": strconv.FormatInt(int64(item.UpdatedInRevision), 10), "updated_at": item.UpdatedAt,
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
