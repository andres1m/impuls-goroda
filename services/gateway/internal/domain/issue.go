package domain

import (
	"errors"
	"strings"
	"time"
)

type IssueType string

const (
	IssueCancelled   IssueType = "cancelled"
	IssueUnreachable IssueType = "unreachable"
	IssueStale       IssueType = "stale"
	IssueUnknown     IssueType = "unknown"
)

type IssueState string

const (
	IssueOpen         IssueState = "open"
	IssueAcknowledged IssueState = "acknowledged"
	IssueResolved     IssueState = "resolved"
)

type IssueDetails struct {
	Code    string
	Message string
}

func (d IssueDetails) Validate() error {
	if strings.TrimSpace(d.Code) == "" || strings.TrimSpace(d.Message) == "" {
		return errors.New("issue code and message are required")
	}
	return nil
}

type RouteIssue struct {
	ID              IssueID
	RouteID         RouteID
	VisitID         *VisitID
	SourceChangeID  *SourceChangeID
	Type            IssueType
	Details         IssueDetails
	State           IssueState
	CatalogRevision *CatalogRevision
	CreatedAt       time.Time
	ResolvedAt      *time.Time
}

func (i RouteIssue) Validate() error {
	if err := requiredID([16]byte(i.ID)); err != nil {
		return err
	}
	if err := requiredID([16]byte(i.RouteID)); err != nil {
		return err
	}
	if i.VisitID != nil {
		if err := requiredID([16]byte(*i.VisitID)); err != nil {
			return err
		}
	}
	if i.SourceChangeID != nil {
		if err := requiredID([16]byte(*i.SourceChangeID)); err != nil {
			return err
		}
	}
	switch i.Type {
	case IssueCancelled, IssueUnreachable, IssueStale, IssueUnknown:
	default:
		return errors.New("invalid issue type")
	}
	if err := i.Details.Validate(); err != nil {
		return err
	}
	switch i.State {
	case IssueOpen, IssueAcknowledged:
		if i.ResolvedAt != nil {
			return errors.New("unresolved issue must not have resolution time")
		}
	case IssueResolved:
		if i.ResolvedAt == nil {
			return errors.New("resolved issue requires resolution time")
		}
	default:
		return errors.New("invalid issue state")
	}
	if i.CatalogRevision != nil {
		if err := i.CatalogRevision.Validate(); err != nil {
			return err
		}
	}
	if i.CreatedAt.IsZero() || (i.ResolvedAt != nil && i.ResolvedAt.Before(i.CreatedAt)) {
		return errors.New("issue timestamps are invalid")
	}
	return nil
}
