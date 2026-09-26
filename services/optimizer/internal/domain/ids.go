package domain

import (
	"errors"
	"fmt"
)

type PlaceID [16]byte
type EntranceID [16]byte
type EventID [16]byte
type SessionID [16]byte
type PriceOfferID [16]byte
type VisitID [16]byte
type SourceRecordID [16]byte

type CatalogRevision int64

func (r CatalogRevision) Validate() error {
	if r < 0 {
		return errors.New("catalog revision must not be negative")
	}
	return nil
}

func requireID[T ~[16]byte](id T, name string) error {
	if id == (T{}) {
		return fmt.Errorf("%s id is required", name)
	}
	return nil
}

func optionalID[T ~[16]byte](id *T, name string) error {
	if id == nil {
		return nil
	}
	return requireID(*id, name)
}
