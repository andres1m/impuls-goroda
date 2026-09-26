package postgres

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIdentityRepositoryIntegration(t *testing.T) {
	databaseURL := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("GATEWAY_TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	transactor, err := NewTransactor(pool)
	if err != nil {
		t.Fatal(err)
	}
	queries, err := NewQueries(pool)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	userID := domain.UserID(randomID(t))
	sessionID := domain.SessionID(randomID(t))
	tokenHash := randomHash(t)
	account := domain.UserAccount{
		ID:         userID,
		MaxUserID:  fmt.Sprintf("integration:%x", userID),
		State:      domain.AccountActive,
		Kind:       domain.AccountMax,
		CreatedAt:  now,
		LastSeenAt: now,
	}
	session := domain.AuthSession{
		ID:        sessionID,
		UserID:    userID,
		TokenHash: tokenHash,
		IssuedVia: domain.SessionFromMax,
		CreatedAt: now,
		ExpiresAt: now.Add(24 * time.Hour),
	}

	storedAccount, storedSession, err := transactor.IssueMaxSession(ctx, account, session)
	if err != nil {
		t.Fatal(err)
	}
	if storedAccount.ID != userID || storedSession.UserID != userID {
		t.Fatalf("stored identity mismatch: %+v %+v", storedAccount, storedSession)
	}
	pair, err := queries.FindSessionByTokenHash(ctx, tokenHash)
	if err != nil {
		t.Fatal(err)
	}
	if !pair.Session.ValidFor(pair.Account, now.Add(time.Minute)) {
		t.Fatal("stored session is not valid")
	}
	revoked, err := queries.RevokeSession(ctx, sessionID, userID, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if revoked.RevokedAt == nil {
		t.Fatal("session was not revoked")
	}

	rollbackUserID := domain.UserID(randomID(t))
	rollbackAccount := account
	rollbackAccount.ID = rollbackUserID
	rollbackAccount.MaxUserID = fmt.Sprintf("integration:%x", rollbackUserID)
	invalidSession := session
	invalidSession.ID = domain.SessionID(randomID(t))
	invalidSession.UserID = rollbackUserID
	invalidSession.TokenHash = [32]byte{}
	if _, _, err := transactor.IssueMaxSession(ctx, rollbackAccount, invalidSession); err == nil {
		t.Fatal("invalid session did not roll back")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM identity.user_account WHERE max_user_id = $1`, rollbackAccount.MaxUserID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rolled back account count = %d", count)
	}

	testUserID := domain.UserID(randomID(t))
	testAccount := domain.UserAccount{
		ID:         testUserID,
		MaxUserID:  fmt.Sprintf("test:integration:%x", testUserID),
		State:      domain.AccountActive,
		Kind:       domain.AccountTest,
		CreatedAt:  now,
		LastSeenAt: now,
	}
	testSession := domain.AuthSession{
		ID:        domain.SessionID(randomID(t)),
		UserID:    testUserID,
		TokenHash: randomHash(t),
		IssuedVia: domain.SessionFromTest,
		CreatedAt: now,
		ExpiresAt: now.Add(time.Minute),
	}
	storedTestAccount, _, err := transactor.IssueTestSession(ctx, testAccount, testSession)
	if err != nil {
		t.Fatal(err)
	}
	foundTestAccount, err := queries.FindTestAccount(ctx, testAccount.MaxUserID)
	if err != nil {
		t.Fatal(err)
	}
	if foundTestAccount.ID != storedTestAccount.ID {
		t.Fatal("test account lookup returned another account")
	}
	deleted, err := queries.DeleteExpiredSessions(ctx, now.Add(2*time.Minute), 100)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted sessions = %d, want 1", deleted)
	}

	if _, err := pool.Exec(ctx, `UPDATE identity.user_account SET account_state = 'disabled' WHERE id = $1`, encodeUUID([16]byte(userID))); err != nil {
		t.Fatal(err)
	}
	disabledSession := session
	disabledSession.ID = domain.SessionID(randomID(t))
	disabledSession.TokenHash = randomHash(t)
	if _, _, err := transactor.IssueMaxSession(ctx, account, disabledSession); !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("disabled account issue error = %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM identity.auth_session WHERE token_hash = $1`, disabledSession.TokenHash[:]).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("disabled account received a session")
	}
}

func TestRuntimePrivilegeMatrix(t *testing.T) {
	urls := map[string]string{
		"gateway_svc":   os.Getenv("GATEWAY_TEST_DATABASE_URL"),
		"optimizer_svc": os.Getenv("OPTIMIZER_TEST_DATABASE_URL"),
		"syncer_svc":    os.Getenv("SYNCER_TEST_DATABASE_URL"),
	}
	for role, url := range urls {
		if url == "" {
			t.Skipf("database URL for %s is not set", role)
		}
	}

	tests := []struct {
		role      string
		privilege string
		object    string
		want      bool
	}{
		{"gateway_svc", "SELECT", "catalog.place", true},
		{"gateway_svc", "UPDATE", "catalog.place", false},
		{"gateway_svc", "SELECT", "identity.user_account", true},
		{"gateway_svc", "DELETE", "planning.route", false},
		{"optimizer_svc", "SELECT", "catalog.place", true},
		{"optimizer_svc", "SELECT", "identity.user_account", false},
		{"optimizer_svc", "INSERT", "catalog.place", false},
		{"syncer_svc", "UPDATE", "catalog.place", true},
		{"syncer_svc", "SELECT", "identity.user_account", false},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, tc := range tests {
		t.Run(tc.role+"_"+tc.privilege+"_"+tc.object, func(t *testing.T) {
			pool, err := pgxpool.New(ctx, urls[tc.role])
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			objectParts := strings.SplitN(tc.object, ".", 2)
			if len(objectParts) != 2 {
				t.Fatalf("invalid table name %q", tc.object)
			}
			var got bool
			if err := pool.QueryRow(ctx, `
SELECT has_table_privilege(current_user, c.oid, $3)
FROM pg_catalog.pg_class AS c
JOIN pg_catalog.pg_namespace AS n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname = $2`, objectParts[0], objectParts[1], tc.privilege).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("has_table_privilege(%s, %s) = %v, want %v", tc.object, tc.privilege, got, tc.want)
			}
		})
	}

	for role, url := range urls {
		t.Run(role+"_attributes", func(t *testing.T) {
			pool, err := pgxpool.New(ctx, url)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			var superuser, createDB, createRole bool
			if err := pool.QueryRow(ctx, `SELECT rolsuper, rolcreatedb, rolcreaterole FROM pg_roles WHERE rolname = current_user`).Scan(&superuser, &createDB, &createRole); err != nil {
				t.Fatal(err)
			}
			if superuser || createDB || createRole {
				t.Fatalf("unsafe role attributes: superuser=%v createdb=%v createrole=%v", superuser, createDB, createRole)
			}
		})
	}

	gatewayPool, err := pgxpool.New(ctx, urls["gateway_svc"])
	if err != nil {
		t.Fatal(err)
	}
	defer gatewayPool.Close()
	var canPurge bool
	if err := gatewayPool.QueryRow(ctx, `SELECT has_function_privilege(current_user, 'planning.purge_route(uuid, uuid)', 'EXECUTE')`).Scan(&canPurge); err != nil {
		t.Fatal(err)
	}
	if !canPurge {
		t.Fatal("gateway cannot execute planning.purge_route")
	}
}

func randomID(t *testing.T) [16]byte {
	t.Helper()
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return id
}

func randomHash(t *testing.T) [32]byte {
	t.Helper()
	var hash [32]byte
	if _, err := rand.Read(hash[:]); err != nil {
		t.Fatal(err)
	}
	return hash
}
