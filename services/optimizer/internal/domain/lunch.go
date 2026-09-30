package domain

import (
	"errors"
	"slices"
	"strings"
	"time"
)

type LunchMetadata struct {
	AfterVisitID VisitID
	Duration     time.Duration
}

func (m LunchMetadata) Validate() error {
	if err := requireID(m.AfterVisitID, "lunch anchor"); err != nil {
		return err
	}
	if m.Duration < 45*time.Minute || m.Duration > time.Hour {
		return errors.New("lunch duration must be 45 to 60 minutes")
	}
	return nil
}

type ExternalVenueSnapshot struct {
	Provider          string
	ExternalID        string
	Title             string
	Position          Coordinate
	Address           string
	ObservedAt        time.Time
	Price             Price
	Availability      string
	HoursVerification VerificationStatus
}

func (v ExternalVenueSnapshot) Validate() error {
	if strings.TrimSpace(v.Provider) == "" || strings.TrimSpace(v.ExternalID) == "" || strings.TrimSpace(v.Title) == "" || v.ObservedAt.IsZero() {
		return errors.New("external venue identity and observation are required")
	}
	if err := v.Position.Validate(); err != nil {
		return err
	}
	if err := v.Price.Validate(); err != nil {
		return err
	}
	if v.Availability != "unknown" {
		return errors.New("external venue availability must be unknown")
	}
	if v.HoursVerification != VerificationUnknown {
		return errors.New("external venue hours must be unverified")
	}
	return nil
}

type LunchStop struct {
	VisitID        VisitID
	AfterVisitID   VisitID
	Duration       time.Duration
	External       *ExternalVenueSnapshot
	CatalogVisitID *VisitID
}

type LunchTrigger struct {
	SchemaVersion uint32
	Stops         []LunchStop
}

func (t LunchTrigger) validate(base *Plan) error {
	if t.SchemaVersion != 1 || len(t.Stops) > 8 {
		return errors.New("unsupported lunch schema or too many stops")
	}
	legacy := 0
	for i := range base.Steps {
		s := &base.Steps[i]
		if s.Lunch == nil && !slices.ContainsFunc(t.Stops, func(stop LunchStop) bool { return stop.VisitID == s.VisitID }) {
			for _, c := range s.AppliedConstraints {
				if c.Code == "LUNCH_WINDOW" {
					legacy++
					break
				}
			}
		}
	}
	if len(t.Stops)+legacy > 8 {
		return errors.New("too many lunches including the existing automatic lunch")
	}
	seen := map[VisitID]bool{}
	order := map[VisitID]int{}
	for _, s := range base.Steps {
		seen[s.VisitID] = !isLunchStep(&s)
		order[s.VisitID] = s.Position
	}
	ids := map[VisitID]bool{}
	previous := 0
	for _, stop := range t.Stops {
		if err := requireID(stop.VisitID, "lunch visit"); err != nil {
			return err
		}
		if ids[stop.VisitID] || (seen[stop.VisitID] && !isBaseLunch(base, stop.VisitID)) {
			return errors.New("lunch visit ID is duplicated or belongs to a non-lunch visit")
		}
		if !seen[stop.AfterVisitID] && !ids[stop.AfterVisitID] {
			return errors.New("lunch anchor must precede its stop")
		}
		if stop.AfterVisitID == stop.VisitID {
			return errors.New("lunch cannot anchor itself")
		}
		if order[stop.AfterVisitID] < previous {
			return errors.New("lunch stops are not in route order")
		}
		if stop.Duration < 45*time.Minute || stop.Duration > time.Hour {
			return errors.New("lunch duration must be 45 to 60 minutes")
		}
		if stop.External != nil && stop.CatalogVisitID != nil {
			return errors.New("lunch venue is ambiguous")
		}
		if stop.External != nil {
			if err := stop.External.Validate(); err != nil {
				return err
			}
		}
		if stop.CatalogVisitID != nil {
			if *stop.CatalogVisitID != stop.VisitID || !isCatalogGastroLunch(base, *stop.CatalogVisitID) {
				return errors.New("catalog lunch must reference the same existing gastro lunch visit")
			}
		}
		ids[stop.VisitID] = true
		previous = order[stop.AfterVisitID]
		order[stop.VisitID] = previous
	}
	return nil
}

func isLunchStep(s *Step) bool {
	if s.Lunch != nil {
		return true
	}
	for _, c := range s.AppliedConstraints {
		if c.Code == "LUNCH_WINDOW" {
			return true
		}
	}
	return false
}

func isCatalogGastroLunch(base *Plan, id VisitID) bool {
	for i := range base.Steps {
		s := &base.Steps[i]
		if s.VisitID == id {
			return isLunchStep(s) && s.Kind == StepVisit && s.Catalog != nil && s.Catalog.Category == CategoryGastro
		}
	}
	return false
}

func isBaseLunch(base *Plan, id VisitID) bool {
	for i := range base.Steps {
		if base.Steps[i].VisitID == id {
			return isLunchStep(&base.Steps[i])
		}
	}
	return false
}
