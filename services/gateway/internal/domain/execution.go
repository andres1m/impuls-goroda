package domain

import (
	"errors"
	"time"
)

type ExecutionStatus string

const (
	ExecutionPlanned   ExecutionStatus = "planned"
	ExecutionCompleted ExecutionStatus = "completed"
	ExecutionSkipped   ExecutionStatus = "skipped"
)

type ConfirmationKind string

const (
	ConfirmationUserReported      ConfirmationKind = "user_reported"
	ConfirmationProviderConfirmed ConfirmationKind = "provider_confirmed"
)

type Execution struct {
	RouteID           RouteID
	VisitID           VisitID
	Status            ExecutionStatus
	ActualStartedAt   *time.Time
	ActualEndedAt     *time.Time
	Confirmation      ConfirmationKind
	UpdatedInRevision RouteRevisionNumber
	UpdatedAt         time.Time
}

func (e Execution) Validate() error {
	if err := requiredID([16]byte(e.RouteID)); err != nil {
		return err
	}
	if err := requiredID([16]byte(e.VisitID)); err != nil {
		return err
	}
	if e.Status != ExecutionPlanned && e.Status != ExecutionCompleted && e.Status != ExecutionSkipped {
		return errors.New("invalid execution status")
	}
	if e.Confirmation != ConfirmationUserReported && e.Confirmation != ConfirmationProviderConfirmed {
		return errors.New("invalid execution confirmation kind")
	}
	if e.ActualStartedAt != nil && e.ActualEndedAt != nil && e.ActualEndedAt.Before(*e.ActualStartedAt) {
		return errors.New("execution ends before it starts")
	}
	if err := e.UpdatedInRevision.Validate(); err != nil {
		return err
	}
	if e.UpdatedAt.IsZero() {
		return errors.New("execution update time is required")
	}
	return nil
}
