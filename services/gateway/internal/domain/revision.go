package domain

import "errors"

type RouteRevisionNumber int64
type CatalogRevision int64

func (r RouteRevisionNumber) Validate() error {
	if r < 1 {
		return errors.New("route revision must be positive")
	}
	return nil
}

func (r CatalogRevision) Validate() error {
	if r < 0 {
		return errors.New("catalog revision must not be negative")
	}
	return nil
}
