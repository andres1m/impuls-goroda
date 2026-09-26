package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type fakeDB struct {
	row       pgx.Row
	query     string
	arguments []any
	rowCalls  int
}

func (f *fakeDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected Exec")
}

func (f *fakeDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unexpected Query")
}

func (f *fakeDB) QueryRow(_ context.Context, query string, arguments ...any) pgx.Row {
	f.query = query
	f.arguments = arguments
	f.rowCalls++
	return f.row
}

type fakeRow struct {
	scan func(...any) error
}

func (r fakeRow) Scan(destinations ...any) error {
	return r.scan(destinations...)
}

func TestFindRouteAccessUsesOwnerInTheQuery(t *testing.T) {
	routeID := domain.RouteID{1}
	userID := domain.UserID{2}
	db := &fakeDB{row: fakeRow{scan: func(destinations ...any) error {
		*destinations[0].(*pgtype.UUID) = encodeUUID([16]byte(routeID))
		*destinations[1].(*pgtype.UUID) = encodeUUID([16]byte(userID))
		*destinations[2].(*domain.RouteLifecycle) = domain.RouteDraft
		*destinations[3].(*domain.RouteRevisionNumber) = 3
		return nil
	}}}
	queries, err := NewQueries(db)
	if err != nil {
		t.Fatal(err)
	}

	access, err := queries.FindRouteAccess(context.Background(), routeID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if access.RouteID != routeID || access.OwnerID != userID || access.Revision != 3 {
		t.Fatalf("unexpected access: %+v", access)
	}
	if !strings.Contains(db.query, "WHERE id = $1 AND owner_id = $2") {
		t.Fatalf("owner predicate is missing: %s", db.query)
	}
	if len(db.arguments) != 2 {
		t.Fatalf("arguments = %d, want 2", len(db.arguments))
	}
}

func TestFindRouteAccessMapsMissingRoute(t *testing.T) {
	db := &fakeDB{row: fakeRow{scan: func(...any) error { return pgx.ErrNoRows }}}
	queries, err := NewQueries(db)
	if err != nil {
		t.Fatal(err)
	}

	_, err = queries.FindRouteAccess(context.Background(), domain.RouteID{1}, domain.UserID{2})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestUpsertMaxAccountKeepsStoredDisabledState(t *testing.T) {
	now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	input := domain.UserAccount{
		ID:         domain.UserID{1},
		MaxUserID:  "max-user",
		State:      domain.AccountActive,
		Kind:       domain.AccountMax,
		CreatedAt:  now,
		LastSeenAt: now,
	}
	db := &fakeDB{row: fakeRow{scan: func(destinations ...any) error {
		*destinations[0].(*pgtype.UUID) = encodeUUID([16]byte(input.ID))
		*destinations[1].(*string) = input.MaxUserID
		*destinations[2].(*time.Time) = input.CreatedAt
		*destinations[3].(*time.Time) = input.LastSeenAt
		*destinations[4].(*domain.AccountState) = domain.AccountDisabled
		*destinations[5].(*domain.AccountKind) = domain.AccountMax
		return nil
	}}}
	queries, err := NewQueries(db)
	if err != nil {
		t.Fatal(err)
	}

	stored, err := queries.UpsertMaxAccount(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != domain.AccountDisabled {
		t.Fatalf("state = %q, want disabled", stored.State)
	}
	if strings.Contains(db.query, "account_state = EXCLUDED") {
		t.Fatal("upsert reactivates an existing account")
	}
}

func TestFindSessionMapsAccountAndPreservesContextError(t *testing.T) {
	now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	hash := [32]byte{3}
	sessionID := domain.SessionID{1}
	userID := domain.UserID{2}
	db := &fakeDB{row: fakeRow{scan: func(destinations ...any) error {
		*destinations[0].(*pgtype.UUID) = encodeUUID([16]byte(sessionID))
		*destinations[1].(*pgtype.UUID) = encodeUUID([16]byte(userID))
		*destinations[2].(*[]byte) = append([]byte(nil), hash[:]...)
		*destinations[3].(*domain.SessionIssuer) = domain.SessionFromMax
		*destinations[4].(*pgtype.Text) = pgtype.Text{String: "web", Valid: true}
		*destinations[5].(*time.Time) = now
		*destinations[6].(*time.Time) = now.Add(24 * time.Hour)
		*destinations[7].(*pgtype.Timestamptz) = pgtype.Timestamptz{}
		*destinations[8].(*pgtype.UUID) = encodeUUID([16]byte(userID))
		*destinations[9].(*string) = "max-user"
		*destinations[10].(*time.Time) = now
		*destinations[11].(*time.Time) = now
		*destinations[12].(*domain.AccountState) = domain.AccountActive
		*destinations[13].(*domain.AccountKind) = domain.AccountMax
		return nil
	}}}
	queries, err := NewQueries(db)
	if err != nil {
		t.Fatal(err)
	}

	pair, err := queries.FindSessionByTokenHash(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if pair.Session.ID != sessionID || pair.Session.UserID != pair.Account.ID {
		t.Fatalf("unexpected pair: %+v", pair)
	}
	if pair.Session.Platform == nil || *pair.Session.Platform != "web" {
		t.Fatalf("platform = %v", pair.Session.Platform)
	}

	db.row = fakeRow{scan: func(...any) error { return context.DeadlineExceeded }}
	_, err = queries.FindSessionByTokenHash(context.Background(), hash)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline", err)
	}
}

func TestInvalidInputsDoNotQueryDatabase(t *testing.T) {
	db := &fakeDB{row: fakeRow{scan: func(...any) error { return nil }}}
	queries, err := NewQueries(db)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := queries.FindSessionByTokenHash(context.Background(), [32]byte{}); err == nil {
		t.Fatal("empty token hash accepted")
	}
	if _, err := queries.FindRouteAccess(context.Background(), domain.RouteID{}, domain.UserID{1}); err == nil {
		t.Fatal("empty route ID accepted")
	}
	if _, err := queries.FindTestAccount(context.Background(), "max-user"); err == nil {
		t.Fatal("non-test account accepted")
	}
	if db.rowCalls != 0 {
		t.Fatalf("database queried %d times", db.rowCalls)
	}
}

func TestNewQueriesRejectsNilDatabase(t *testing.T) {
	if _, err := NewQueries(nil); err == nil {
		t.Fatal("nil database accepted")
	}
}
