package domain

import (
	"testing"
	"time"
)

var instant = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

func id() [16]byte { return [16]byte{1} }

func TestSessionValidity(t *testing.T) {
	user := UserAccount{ID: UserID(id()), MaxUserID: "12345", State: AccountActive, Kind: AccountMax, CreatedAt: instant, LastSeenAt: instant}
	session := AuthSession{ID: SessionID(id()), UserID: user.ID, TokenHash: [32]byte{1}, IssuedVia: SessionFromMax, CreatedAt: instant, ExpiresAt: instant.Add(time.Hour)}
	if err := user.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := session.Validate(); err != nil {
		t.Fatal(err)
	}
	if !session.ValidFor(user, instant.Add(time.Minute)) {
		t.Fatal("active session rejected")
	}
	if session.ValidFor(user, session.ExpiresAt) {
		t.Fatal("expiration boundary accepted")
	}
	if session.ValidFor(user, instant.Add(-time.Second)) {
		t.Fatal("session valid before issue")
	}

	disabled := user
	disabled.State = AccountDisabled
	if session.ValidFor(disabled, instant.Add(time.Minute)) {
		t.Fatal("disabled account accepted")
	}
	other := user
	other.ID = UserID([16]byte{2})
	if session.ValidFor(other, instant.Add(time.Minute)) {
		t.Fatal("another user accepted")
	}
	revoked := instant.Add(time.Minute)
	session.RevokedAt = &revoked
	if session.ValidFor(user, instant.Add(2*time.Minute)) {
		t.Fatal("revoked session accepted")
	}
}

func TestIdentityAndRevisionFailures(t *testing.T) {
	user := UserAccount{ID: UserID(id()), MaxUserID: "test:jury", State: AccountActive, Kind: AccountMax, CreatedAt: instant, LastSeenAt: instant}
	if user.Validate() == nil {
		t.Fatal("test namespace accepted for MAX account")
	}
	user.Kind = AccountTest
	if err := user.Validate(); err != nil {
		t.Fatal(err)
	}
	if RouteRevisionNumber(0).Validate() == nil || RouteRevisionNumber(-1).Validate() == nil {
		t.Fatal("invalid route revision accepted")
	}
	if err := CatalogRevision(0).Validate(); err != nil {
		t.Fatal(err)
	}
	if CatalogRevision(-1).Validate() == nil {
		t.Fatal("negative catalog revision accepted")
	}
}

func TestRouteMetadata(t *testing.T) {
	route := Route{ID: RouteID(id()), OwnerID: UserID(id()), City: "perm", Lifecycle: RouteDraft, CurrentRevision: 1, CreatedAt: instant, UpdatedAt: instant}
	if err := route.Validate(); err != nil {
		t.Fatal(err)
	}
	route.CopiedFrom = &CopyOrigin{RouteID: RouteID(id())}
	if route.Validate() == nil {
		t.Fatal("copy without source revision accepted")
	}
	route.CopiedFrom.Revision = 2
	if err := route.Validate(); err != nil {
		t.Fatal(err)
	}
	route.CopiedFrom.RouteID = RouteID{}
	if route.Validate() == nil {
		t.Fatal("copy without source route accepted")
	}
}

func TestVisitCatalogReferences(t *testing.T) {
	visit := RouteVisit{RouteID: RouteID(id()), ID: VisitID(id()), Kind: VisitFreeTime, City: "moscow", CreatedInRevision: 1, CreatedAt: instant}
	if err := visit.Validate(); err != nil {
		t.Fatal(err)
	}
	place := PlaceID(id())
	visit.PlaceID = &place
	if visit.Validate() == nil {
		t.Fatal("free time referencing a place accepted")
	}
	visit.Kind = VisitPlace
	if err := visit.Validate(); err != nil {
		t.Fatal(err)
	}
	session := EventSessionID(id())
	visit.SessionID = &session
	if visit.Validate() == nil {
		t.Fatal("session without event accepted")
	}
	event := EventID(id())
	visit.EventID = &event
	if err := visit.Validate(); err != nil {
		t.Fatal(err)
	}
	place = PlaceID{}
	if visit.Validate() == nil {
		t.Fatal("zero catalog identifier accepted")
	}
}

func TestParticipationEvidenceAndExecutionTime(t *testing.T) {
	p := Participation{RouteID: RouteID(id()), VisitID: VisitID(id()), Status: ParticipationActionRequired, Evidence: EvidenceNone, UpdatedInRevision: 1, UpdatedAt: instant}
	opened := instant.Add(time.Minute)
	p.ExternalLinkOpenedAt = &opened
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if p.Status != ParticipationActionRequired {
		t.Fatal("link changed participation")
	}
	p.Status = ParticipationProviderConfirmed
	if p.Validate() == nil {
		t.Fatal("provider confirmation without evidence accepted")
	}
	p.Evidence = EvidenceProvider
	if p.Validate() == nil {
		t.Fatal("provider confirmation without record accepted")
	}
	record := SourceRecordID(id())
	p.ProviderRecordID = &record
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}

	e := Execution{RouteID: RouteID(id()), VisitID: VisitID(id()), Status: ExecutionCompleted, Confirmation: ConfirmationUserReported, UpdatedInRevision: 1, UpdatedAt: instant}
	end := instant
	start := instant.Add(time.Minute)
	e.ActualStartedAt, e.ActualEndedAt = &start, &end
	if e.Validate() == nil {
		t.Fatal("reverse execution interval accepted")
	}
	e.ActualEndedAt = &start
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestShareBoundaries(t *testing.T) {
	share := RouteShare{ID: ShareID(id()), RouteID: RouteID(id()), TokenHash: [32]byte{1}, CreatedBy: UserID(id()), CreatedAt: instant}
	if !share.ActiveAt(instant) {
		t.Fatal("fresh share rejected")
	}
	if share.ActiveAt(instant.Add(-time.Second)) {
		t.Fatal("share active before creation")
	}
	expires := instant.Add(time.Hour)
	share.ExpiresAt = &expires
	if share.ActiveAt(expires) {
		t.Fatal("expired share accepted")
	}
	revoked := instant.Add(time.Minute)
	share.RevokedAt = &revoked
	if share.ActiveAt(instant.Add(2 * time.Minute)) {
		t.Fatal("revoked share accepted")
	}
}
