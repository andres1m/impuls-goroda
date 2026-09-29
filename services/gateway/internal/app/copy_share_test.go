package app

import (
	"testing"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
)

func TestCopyPreservesExactSessionWithoutAuthorIdentity(t *testing.T) {
	place := d.PlaceID{1}
	event := d.EventID{2}
	session := d.EventSessionID{3}
	base := d.RoutePlanSnapshot{Steps: []d.RouteStep{{VisitID: d.VisitID{4}, Kind: d.VisitPlace,
		Catalog: &d.CatalogSnapshot{PlaceID: &place, EventID: &event, SessionID: &session}}}}
	copied := d.RoutePlanSnapshot{Steps: []d.RouteStep{{VisitID: d.VisitID{5}, Kind: d.VisitPlace,
		Catalog:       &d.CatalogSnapshot{PlaceID: &place, EventID: &event, SessionID: &session},
		Participation: d.ParticipationSnapshot{Evidence: d.EvidenceNone}}}}
	if !copyPreservesVisits(base, copied) {
		t.Fatal("valid independent copy rejected")
	}
	copied.Steps[0].VisitID = base.Steps[0].VisitID
	if copyPreservesVisits(base, copied) {
		t.Fatal("author visit identity accepted")
	}
	copied.Steps[0].VisitID = d.VisitID{5}
	replacement := d.EventSessionID{6}
	copied.Steps[0].Catalog.SessionID = &replacement
	if copyPreservesVisits(base, copied) {
		t.Fatal("different session in same place accepted")
	}
	copied.Steps[0].Catalog.SessionID = &session
	copied.Steps[0].Participation.Evidence = d.EvidenceUser
	if copyPreservesVisits(base, copied) {
		t.Fatal("author confirmation accepted")
	}
}
