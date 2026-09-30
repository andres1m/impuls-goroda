package domain

import (
	"testing"
	"time"
)

func TestLunchTriggerRejectsInvalidOrderedStops(t *testing.T) {
	base := validPlan()
	valid := LunchTrigger{SchemaVersion: 1, Stops: []LunchStop{{VisitID: VisitID{8}, AfterVisitID: base.Steps[0].VisitID, Duration: 45 * time.Minute}, {VisitID: VisitID{9}, AfterVisitID: VisitID{8}, Duration: time.Hour}}}
	if err := valid.validate(&base); err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name   string
		change func(*LunchTrigger)
	}{
		{"version", func(l *LunchTrigger) { l.SchemaVersion = 2 }},
		{"duration", func(l *LunchTrigger) { l.Stops[0].Duration = 44 * time.Minute }},
		{"duplicate", func(l *LunchTrigger) { l.Stops[1].VisitID = l.Stops[0].VisitID }},
		{"forward anchor", func(l *LunchTrigger) { l.Stops[0].AfterVisitID = l.Stops[1].VisitID }},
		{"unknown catalog lunch", func(l *LunchTrigger) { id := VisitID{4}; l.Stops[0].CatalogVisitID = &id }},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			v := valid
			v.Stops = append([]LunchStop(nil), valid.Stops...)
			tc.change(&v)
			if err := v.validate(&base); err == nil {
				t.Fatal("invalid trigger accepted")
			}
		})
	}
}

func TestAddedChangeRequiresNewVisit(t *testing.T) {
	id := VisitID{8}
	c := RouteChange{Kind: ChangeAdded, Scope: ScopeVisit, AfterVisitID: &id, Message: "Lunch added"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.AfterVisitID = nil
	if err := c.Validate(); err == nil {
		t.Fatal("missing new visit accepted")
	}
}
