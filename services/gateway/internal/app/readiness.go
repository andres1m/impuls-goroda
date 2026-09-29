package app

import (
	"context"
	"time"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
)

type CapabilityStatus struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type ReadinessCapabilities struct {
	ReadSavedRoutes CapabilityStatus `json:"read_saved_routes"`
	MutateRoutes    CapabilityStatus `json:"mutate_routes"`
	Optimize        CapabilityStatus `json:"optimize"`
	CatalogUpdates  CapabilityStatus `json:"catalog_updates"`
}

type OperationReadiness struct {
	Status       string                `json:"status"`
	CheckedAt    time.Time             `json:"checked_at"`
	Capabilities ReadinessCapabilities `json:"capabilities"`
}

type optimizerTransportChecker interface {
	CheckTransport(context.Context) error
}

func (r *Runtime) Readiness(ctx context.Context) OperationReadiness {
	result := OperationReadiness{Status: "unavailable", Capabilities: ReadinessCapabilities{
		ReadSavedRoutes: CapabilityStatus{Status: "unavailable", Reason: "ROUTE_STORAGE_UNAVAILABLE"},
		MutateRoutes:    CapabilityStatus{Status: "unavailable", Reason: "ROUTE_STORAGE_UNAVAILABLE"},
		Optimize:        CapabilityStatus{Status: "unavailable", Reason: "ROUTE_STORAGE_UNAVAILABLE"},
		CatalogUpdates:  CapabilityStatus{Status: "unavailable", Reason: "CATALOG_UPDATES_NOT_CONNECTED"},
	}}
	if r.HealthCheck(ctx) != nil {
		result.CheckedAt = r.clock().UTC()
		return result
	}
	if r.cfg.LifecycleEnabled {
		result.Capabilities.CatalogUpdates = CapabilityStatus{Status: "degraded", Reason: "CATALOG_LIFECYCLE_DELIVERY_UNVERIFIED"}
	}
	type storageResult struct {
		state postgres.RouteStorageReadiness
		err   error
	}
	storage := make(chan storageResult, 1)
	transport := make(chan CapabilityStatus, 1)
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	go func() {
		state, err := r.queries.RouteStorageReadiness(probeCtx)
		storage <- storageResult{state: state, err: err}
	}()
	go func() {
		status := CapabilityStatus{Status: "unavailable", Reason: "OPTIMIZER_UNAVAILABLE"}
		if checker, ok := r.cfg.Optimizer.(optimizerTransportChecker); ok {
			if checker.CheckTransport(probeCtx) == nil {
				status = CapabilityStatus{Status: "degraded", Reason: "OPTIMIZER_CAPABILITY_UNVERIFIED"}
			}
		} else if r.cfg.Optimizer != nil {
			status = CapabilityStatus{Status: "degraded", Reason: "OPTIMIZER_CAPABILITY_UNVERIFIED"}
		}
		transport <- status
	}()
	var stored storageResult
	var optimizer CapabilityStatus
	select {
	case stored = <-storage:
	case <-probeCtx.Done():
		stored.err = probeCtx.Err()
	}
	select {
	case optimizer = <-transport:
	case <-probeCtx.Done():
		optimizer = CapabilityStatus{Status: "unavailable", Reason: "OPTIMIZER_UNAVAILABLE"}
	}
	if stored.err == nil && stored.state.Readable {
		result.Capabilities.ReadSavedRoutes = CapabilityStatus{Status: "available"}
		result.Status = "degraded"
		if stored.state.Writable {
			result.Capabilities.MutateRoutes = CapabilityStatus{Status: "degraded", Reason: "COMPUTE_MUTATIONS_UNVERIFIED"}
			result.Capabilities.Optimize = optimizer
		} else {
			result.Capabilities.MutateRoutes.Reason = "ROUTE_STORAGE_WRITE_UNAVAILABLE"
			result.Capabilities.Optimize.Reason = "ROUTE_STORAGE_WRITE_UNAVAILABLE"
		}
	}
	result.CheckedAt = r.clock().UTC()
	return result
}
