package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type OptimizeRequest struct {
	City        string
	Timezone    string
	Start       time.Time
	End         time.Time
	Origin      Coordinate
	Destination *Coordinate
	Constraints RouteConstraints
}

func (r OptimizeRequest) Validate() error {
	if err := validateCityZone(r.City, r.Timezone); err != nil {
		return err
	}
	if r.Start.IsZero() || !r.End.After(r.Start) {
		return errors.New("planning interval is invalid")
	}
	if err := r.Origin.Validate(); err != nil {
		return err
	}
	if r.Destination != nil {
		if err := r.Destination.Validate(); err != nil {
			return err
		}
	}
	return r.Constraints.Validate()
}

func validateCityZone(city, timezone string) error {
	if strings.TrimSpace(city) == "" {
		return errors.New("city is required")
	}
	if strings.TrimSpace(timezone) == "" {
		return errors.New("timezone is required")
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return fmt.Errorf("unknown timezone: %w", err)
	}
	return nil
}

type ExecutionStatus string

const (
	ExecutionCompleted ExecutionStatus = "completed"
	ExecutionSkipped   ExecutionStatus = "skipped"
)

// VisitExecution is the actual history of a visit; visits without one are still planned.
type VisitExecution struct {
	VisitID     VisitID
	Status      ExecutionStatus
	ActualStart *time.Time
	ActualEnd   *time.Time
}

func (e VisitExecution) Validate() error {
	if err := requireID(e.VisitID, "visit"); err != nil {
		return err
	}
	switch e.Status {
	case ExecutionCompleted:
		if e.ActualStart == nil || e.ActualEnd == nil {
			return errors.New("completed visit requires actual start and end")
		}
	case ExecutionSkipped:
	default:
		return errors.New("invalid execution status")
	}
	if !validOptionalTime(e.ActualStart) || !validOptionalTime(e.ActualEnd) {
		return errors.New("actual visit time is invalid")
	}
	if e.ActualStart != nil && e.ActualEnd != nil && !e.ActualEnd.After(*e.ActualStart) {
		return errors.New("actual visit interval is invalid")
	}
	return nil
}

type RecomputeRequest struct {
	City        string
	Timezone    string
	Base        Plan
	Constraints RouteConstraints
	History     []VisitExecution
	Trigger     Trigger
}

func (r RecomputeRequest) Validate() error {
	if err := validateCityZone(r.City, r.Timezone); err != nil {
		return err
	}
	if err := r.Base.Validate(); err != nil {
		return fmt.Errorf("base plan: %w", err)
	}
	if err := r.Constraints.Validate(); err != nil {
		return err
	}
	if err := r.Base.ValidateBudget(r.Constraints.Budget); err != nil {
		return fmt.Errorf("base plan: %w", err)
	}
	for _, execution := range r.History {
		if err := execution.Validate(); err != nil {
			return err
		}
		if !r.Base.hasVisit(execution.VisitID) {
			return errors.New("history refers to a visit outside the base plan")
		}
	}
	if r.Trigger == nil {
		return errors.New("recompute trigger is required")
	}
	return r.Trigger.validate(r.Base)
}

// Trigger is the reason for a recompute; the set of triggers is closed.
type Trigger interface {
	validate(base Plan) error
}

type DelayMode string

const (
	DelayAlreadyDelayed DelayMode = "already_delayed"
	DelayFutureWait     DelayMode = "future_wait"
)

type PositionSource string

const (
	PositionDevice PositionSource = "device"
	PositionManual PositionSource = "manual"
)

type DelayTrigger struct {
	Mode DelayMode
	// Resolved by the caller, so one request always yields one result.
	EffectiveStart time.Time
	Position       Coordinate
	PositionSource PositionSource
}

func (t DelayTrigger) validate(Plan) error {
	if t.Mode != DelayAlreadyDelayed && t.Mode != DelayFutureWait {
		return errors.New("invalid delay mode")
	}
	if t.EffectiveStart.IsZero() {
		return errors.New("effective start is required")
	}
	if t.PositionSource != PositionDevice && t.PositionSource != PositionManual {
		return errors.New("invalid position source")
	}
	return t.Position.Validate()
}

type CancellationTrigger struct {
	VisitIDs []VisitID
	// The result must be computed on this catalog revision or a newer one.
	MinCatalogRevision CatalogRevision
}

func (t CancellationTrigger) validate(base Plan) error {
	if len(t.VisitIDs) == 0 {
		return errors.New("cancellation requires affected visits")
	}
	for _, id := range t.VisitIDs {
		if err := requireBaseVisit(base, id); err != nil {
			return err
		}
	}
	return t.MinCatalogRevision.Validate()
}

type RemovalMode string

const (
	RemovalRebuild  RemovalMode = "rebuild"
	RemovalFreeTime RemovalMode = "free_time"
)

type RemovalTrigger struct {
	VisitID VisitID
	Mode    RemovalMode
}

func (t RemovalTrigger) validate(base Plan) error {
	if t.Mode != RemovalRebuild && t.Mode != RemovalFreeTime {
		return errors.New("invalid removal mode")
	}
	return requireBaseVisit(base, t.VisitID)
}

type PinKind string

const (
	PinPreferred  PinKind = "preferred"
	PinObligation PinKind = "obligation"
	PinNone       PinKind = "none"
)

type PinTrigger struct {
	VisitID VisitID
	Kind    PinKind
}

func (t PinTrigger) validate(base Plan) error {
	switch t.Kind {
	case PinPreferred, PinObligation, PinNone:
	default:
		return errors.New("invalid pin kind")
	}
	return requireBaseVisit(base, t.VisitID)
}

func requireBaseVisit(base Plan, id VisitID) error {
	if err := requireID(id, "visit"); err != nil {
		return err
	}
	if !base.hasVisit(id) {
		return errors.New("trigger refers to a visit outside the base plan")
	}
	return nil
}
