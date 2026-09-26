package domain

import (
	"errors"
	"fmt"
	"time"
)

type DataFreshness struct {
	// The weakest mode among the facts the result relies on.
	DataMode        DataMode
	DataAsOf        *time.Time
	CatalogRevision CatalogRevision
}

func (f DataFreshness) Validate() error {
	if err := f.DataMode.Validate(); err != nil {
		return err
	}
	if !validOptionalTime(f.DataAsOf) {
		return errors.New("data time is invalid")
	}
	return f.CatalogRevision.Validate()
}

type OptimizeResult struct {
	Status ResultStatus
	// At most one route per archetype, so never more than three.
	Routes          []Plan
	Warnings        []Warning
	Conflicts       []Conflict
	Data            DataFreshness
	ComputationTime time.Duration
}

func (r OptimizeResult) Validate() error {
	switch r.Status {
	case ResultReady, ResultPartial:
		if len(r.Routes) == 0 {
			return errors.New("feasible result requires a route")
		}
	case ResultNoFeasibleRoute, ResultConflict:
		if len(r.Routes) > 0 {
			return errors.New("infeasible result must not have routes")
		}
		if r.Status == ResultConflict && len(r.Conflicts) == 0 {
			return errors.New("conflict result requires a conflict")
		}
	default:
		return errors.New("invalid result status")
	}
	archetypes := make(map[Archetype]struct{}, len(r.Routes))
	for i, route := range r.Routes {
		if err := route.Validate(); err != nil {
			return fmt.Errorf("route %d: %w", i+1, err)
		}
		if _, repeated := archetypes[route.Archetype]; repeated {
			return errors.New("result repeats an archetype")
		}
		archetypes[route.Archetype] = struct{}{}
	}
	for _, warning := range r.Warnings {
		if err := warning.Validate(); err != nil {
			return err
		}
		if warning.Scope != ScopeRoute {
			return errors.New("result warning must not target a single visit or leg")
		}
	}
	return validateOutcome(r.Conflicts, r.Data, r.ComputationTime)
}

func validateOutcome(conflicts []Conflict, data DataFreshness, elapsed time.Duration) error {
	for _, conflict := range conflicts {
		if err := conflict.Validate(); err != nil {
			return err
		}
	}
	if elapsed < 0 {
		return errors.New("computation time must not be negative")
	}
	return data.Validate()
}

type RecomputeStatus string

const (
	RecomputeProposed  RecomputeStatus = "proposed"
	RecomputeUnchanged RecomputeStatus = "unchanged"
	RecomputeConflict  RecomputeStatus = "conflict"
)

type RecomputeResult struct {
	Status          RecomputeStatus
	Candidate       *Plan
	Changes         []RouteChange
	Conflicts       []Conflict
	Data            DataFreshness
	ComputationTime time.Duration
}

func (r RecomputeResult) Validate() error {
	switch r.Status {
	case RecomputeProposed:
		if r.Candidate == nil {
			return errors.New("proposed recompute requires a candidate")
		}
	case RecomputeUnchanged:
		if r.Candidate != nil || len(r.Changes) > 0 {
			return errors.New("unchanged recompute must not carry a candidate or changes")
		}
	case RecomputeConflict:
		if len(r.Conflicts) == 0 {
			return errors.New("conflict recompute requires a conflict")
		}
	default:
		return errors.New("invalid recompute status")
	}
	if r.Candidate != nil {
		if err := r.Candidate.Validate(); err != nil {
			return fmt.Errorf("candidate: %w", err)
		}
	}
	for _, change := range r.Changes {
		if err := change.Validate(); err != nil {
			return err
		}
	}
	return validateOutcome(r.Conflicts, r.Data, r.ComputationTime)
}
