package domain

import (
	"errors"
	"time"
)

type RouteLifecycle string

const (
	RouteDraft RouteLifecycle = "draft"
	RouteSaved RouteLifecycle = "saved"
)

type CopyOrigin struct {
	RouteID  RouteID
	Revision RouteRevisionNumber
}

type Route struct {
	ID              RouteID
	OwnerID         UserID
	City            string
	Lifecycle       RouteLifecycle
	CurrentRevision RouteRevisionNumber
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DraftExpiresAt  *time.Time
	CopiedFrom      *CopyOrigin
}

func (r Route) Validate() error {
	if err := requiredID([16]byte(r.ID)); err != nil {
		return err
	}
	if err := requiredID([16]byte(r.OwnerID)); err != nil {
		return err
	}
	if r.City == "" {
		return errors.New("route city is required")
	}
	if r.Lifecycle != RouteDraft && r.Lifecycle != RouteSaved {
		return errors.New("invalid route lifecycle")
	}
	if err := r.CurrentRevision.Validate(); err != nil {
		return err
	}
	if r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() || r.UpdatedAt.Before(r.CreatedAt) {
		return errors.New("invalid route timestamps")
	}
	if r.CopiedFrom != nil {
		if err := requiredID([16]byte(r.CopiedFrom.RouteID)); err != nil {
			return err
		}
		if err := r.CopiedFrom.Revision.Validate(); err != nil {
			return err
		}
	}
	return nil
}
