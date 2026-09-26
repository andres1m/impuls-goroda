package domain

import (
	"errors"
	"time"
)

type ParticipationStatus string

const (
	ParticipationNotRequired       ParticipationStatus = "not_required"
	ParticipationActionRequired    ParticipationStatus = "action_required"
	ParticipationUserReported      ParticipationStatus = "user_reported_confirmed"
	ParticipationProviderConfirmed ParticipationStatus = "provider_confirmed"
	ParticipationUnavailable       ParticipationStatus = "unavailable"
)

type EvidenceSource string

const (
	EvidenceNone     EvidenceSource = "none"
	EvidenceUser     EvidenceSource = "user"
	EvidenceProvider EvidenceSource = "provider"
)

type Participation struct {
	RouteID              RouteID
	VisitID              VisitID
	Status               ParticipationStatus
	Evidence             EvidenceSource
	ProviderRecordID     *SourceRecordID
	PrivateReference     *string
	ExternalLinkOpenedAt *time.Time
	UpdatedInRevision    RouteRevisionNumber
	UpdatedAt            time.Time
}

func validParticipationStatus(status ParticipationStatus) bool {
	switch status {
	case ParticipationNotRequired, ParticipationActionRequired, ParticipationUserReported, ParticipationProviderConfirmed, ParticipationUnavailable:
		return true
	default:
		return false
	}
}

func (p Participation) Validate() error {
	if err := requiredID([16]byte(p.RouteID)); err != nil {
		return err
	}
	if err := requiredID([16]byte(p.VisitID)); err != nil {
		return err
	}
	if !validParticipationStatus(p.Status) {
		return errors.New("invalid participation status")
	}
	switch p.Evidence {
	case EvidenceNone, EvidenceUser, EvidenceProvider:
	default:
		return errors.New("invalid participation evidence source")
	}
	if p.Status == ParticipationProviderConfirmed && (p.Evidence != EvidenceProvider || p.ProviderRecordID == nil) {
		return errors.New("provider confirmation requires provider evidence")
	}
	if p.ProviderRecordID != nil && *p.ProviderRecordID == (SourceRecordID{}) {
		return errors.New("provider record identifier cannot be empty")
	}
	if err := p.UpdatedInRevision.Validate(); err != nil {
		return err
	}
	if p.UpdatedAt.IsZero() {
		return errors.New("participation update time is required")
	}
	return nil
}
