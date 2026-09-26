package domain

import (
	"errors"
	"time"
)

type RouteShare struct {
	ID        ShareID
	RouteID   RouteID
	TokenHash [32]byte
	CreatedBy UserID
	CreatedAt time.Time
	RevokedAt *time.Time
	ExpiresAt *time.Time
}

func (s RouteShare) Validate() error {
	if err := requiredID([16]byte(s.ID)); err != nil {
		return err
	}
	if err := requiredID([16]byte(s.RouteID)); err != nil {
		return err
	}
	if err := requiredID([16]byte(s.CreatedBy)); err != nil {
		return err
	}
	if s.TokenHash == ([32]byte{}) {
		return errors.New("share token hash is required")
	}
	if s.CreatedAt.IsZero() {
		return errors.New("share creation time is required")
	}
	return nil
}

func (s RouteShare) ActiveAt(now time.Time) bool {
	return s.Validate() == nil && !now.Before(s.CreatedAt) && s.RevokedAt == nil &&
		(s.ExpiresAt == nil || now.Before(*s.ExpiresAt))
}
