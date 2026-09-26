package domain

import "errors"

type UserID [16]byte
type SessionID [16]byte
type RouteID [16]byte
type VisitID [16]byte
type ShareID [16]byte
type ProposalID [16]byte
type IssueID [16]byte
type SourceChangeID [16]byte
type PlaceID [16]byte
type EntranceID [16]byte
type EventID [16]byte
type EventSessionID [16]byte
type PriceOfferID [16]byte
type SourceRecordID [16]byte

func requiredID(id [16]byte) error {
	if id == ([16]byte{}) {
		return errors.New("identifier is required")
	}
	return nil
}
