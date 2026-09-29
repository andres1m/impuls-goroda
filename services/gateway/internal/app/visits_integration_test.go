package app

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

type visitIntegrationFixture struct {
	gateway   *pgxpool.Pool
	syncer    *pgxpool.Pool
	service   *Runtime
	user      domain.UserID
	route     domain.RouteID
	visit     domain.VisitID
	place     domain.PlaceID
	proposal  [16]byte
	routeText string
	visitText string
	now       time.Time
}

func fillRandomUUID(t *testing.T, id []byte) {
	t.Helper()
	if _, err := rand.Read(id); err != nil {
		t.Fatal(err)
	}
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
}

func TestVisitActionsIntegration(t *testing.T) {
	gatewayURL, syncerURL := os.Getenv("GATEWAY_TEST_DATABASE_URL"), os.Getenv("SYNCER_TEST_DATABASE_URL")
	if gatewayURL == "" || syncerURL == "" {
		t.Skip("gateway and syncer test database URLs are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	gateway, err := pgxpool.New(ctx, gatewayURL)
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	syncer, err := pgxpool.New(ctx, syncerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer syncer.Close()

	fixture := setupVisitIntegrationFixture(ctx, t, gateway, syncer)
	target := VisitCommand{
		ActorID:          fixture.user,
		RouteID:          fixture.route,
		VisitID:          fixture.visit,
		ExpectedRevision: 1,
		Key:              [16]byte{1},
	}
	assertLinkAndUserParticipation(ctx, t, fixture, &target)
	assertClearAndExecution(ctx, t, fixture, &target)
	assertProviderPrecedence(ctx, t, fixture, &target)
}

func setupVisitIntegrationFixture(
	ctx context.Context,
	t *testing.T,
	gateway, syncer *pgxpool.Pool,
) *visitIntegrationFixture {
	t.Helper()
	transactor, err := postgres.NewTransactor(gateway)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := postgres.NewCommandExecutor(transactor)
	if err != nil {
		t.Fatal(err)
	}
	f := &visitIntegrationFixture{
		gateway: gateway,
		syncer:  syncer,
		service: &Runtime{
			commands: executor,
			clock:    func() time.Time { return time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC) },
		},
		now: time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC),
	}
	for _, id := range [][]byte{f.user[:], f.route[:], f.visit[:], f.place[:], f.proposal[:]} {
		fillRandomUUID(t, id)
	}
	userText := idString(f.user[:])
	f.routeText = idString(f.route[:])
	f.visitText = idString(f.visit[:])
	placeText := idString(f.place[:])
	queries, err := postgres.NewQueries(gateway)
	if err != nil {
		t.Fatal(err)
	}
	account := domain.UserAccount{
		ID: f.user, MaxUserID: "test:visits:" + userText, State: domain.AccountActive,
		Kind: domain.AccountTest, CreatedAt: f.now, LastSeenAt: f.now,
	}
	if _, upsertErr := queries.UpsertTestAccount(ctx, &account); upsertErr != nil {
		t.Fatal(upsertErr)
	}
	if _, execErr := syncer.Exec(ctx, `INSERT INTO catalog.place (
    id, city, title, normalized_title, coordinates, data_mode, is_active,
    review_required, created_at, updated_at
) VALUES ($1, 'moscow', 'Fixture place', 'fixture place',
    ST_SetSRID(ST_MakePoint(37.6, 55.7), 4326), 'synthetic', true, false, $2, $2)`,
		placeText, f.now); execErr != nil {
		t.Fatal(execErr)
	}
	seedVisitPlanningData(ctx, t, gateway, f, userText, placeText)
	return f
}

func seedVisitPlanningData(
	ctx context.Context,
	t *testing.T,
	gateway *pgxpool.Pool,
	f *visitIntegrationFixture,
	userText, placeText string,
) {
	t.Helper()
	tx, err := gateway.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO planning.route (id, owner_id, city, lifecycle_state, current_revision, created_at, updated_at)
VALUES ($1, $2, 'moscow', 'draft', 1, $3, $3)`, []any{f.routeText, userText, f.now}},
		{`INSERT INTO planning.route_revision (
    route_id, revision, lifecycle_state, archetype_id, timezone, start_at, end_at,
    origin, input_schema_version, constraints, catalog_revision, result_status,
    warnings, cost_summary, mutation_kind, created_at
) VALUES ($1, 1, 'draft', 'fixture', 'Europe/Moscow', $2, $2::timestamptz + interval '1 hour',
    ST_SetSRID(ST_MakePoint(37.6, 55.7), 4326), 1, '{}'::jsonb, 0, 'READY',
    '[]'::jsonb, '{}'::jsonb, 'create', $2)`, []any{f.routeText, f.now}},
		{`INSERT INTO planning.route_visit (
    route_id, visit_id, visit_kind, city, place_id, created_in_revision, created_at
) VALUES ($1, $2, 'visit', 'moscow', $3, 1, $4)`, []any{f.routeText, f.visitText, placeText, f.now}},
		{`INSERT INTO planning.route_step (
    route_id, revision, position, visit_id, arrival_at, visit_start_at, visit_end_at,
    departure_at, min_duration_s, is_pinned, is_obligation,
    participation_snapshot, catalog_snapshot, cost_snapshot, applied_constraints
) VALUES ($1, 1, 1, $2, $3, $3::timestamptz + interval '10 minutes',
    $3::timestamptz + interval '30 minutes', $3::timestamptz + interval '40 minutes',
    0, false, false, '{"status":"action_required","evidence":"none"}'::jsonb,
    '{}'::jsonb, '{}'::jsonb, '{}'::jsonb)`, []any{f.routeText, f.visitText, f.now}},
		{`INSERT INTO planning.participation (
    route_id, visit_id, status, evidence_source, updated_in_revision, updated_at
) VALUES ($1, $2, 'action_required', 'none', 1, $3)`, []any{f.routeText, f.visitText, f.now}},
		{`INSERT INTO planning.execution (
    route_id, visit_id, status, confirmation_kind, updated_in_revision, updated_at
) VALUES ($1, $2, 'planned', 'user_reported', 1, $3)`, []any{f.routeText, f.visitText, f.now}},
		{`INSERT INTO planning.route_proposal (
    id, route_id, base_revision, base_catalog_revision, reason, state,
    candidate_schema_version, candidate, changes, conflicts, created_at
) VALUES ($1, $2, 1, 0, 'delay', 'pending', 1,
    '{}'::jsonb, '[]'::jsonb, '[]'::jsonb, $3)`, []any{idString(f.proposal[:]), f.routeText, f.now}},
	}
	for _, statement := range statements {
		if _, execErr := tx.Exec(ctx, statement.sql, statement.args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		t.Fatal(commitErr)
	}
}

func assertLinkAndUserParticipation(
	ctx context.Context,
	t *testing.T,
	f *visitIntegrationFixture,
	target *VisitCommand,
) {
	t.Helper()
	otherOwner := *target
	otherOwner.ActorID = domain.UserID{42}
	if _, err := f.service.UpdateParticipation(
		ctx,
		otherOwner,
		ParticipationInput{Action: "external_link_opened"},
	); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("other owner error=%v", err)
	}
	opened, err := f.service.UpdateParticipation(ctx, *target, ParticipationInput{Action: "external_link_opened"})
	if err != nil {
		t.Fatal(err)
	}
	checkStatus(t, &opened, "UNCHANGED", "1")
	target.Key[0] = 2
	openedAgain, err := f.service.UpdateParticipation(ctx, *target, ParticipationInput{Action: "external_link_opened"})
	if err != nil {
		t.Fatal(err)
	}
	checkStatus(t, &openedAgain, "UNCHANGED", "1")
	if !sameJSON(opened.ResponseBody, openedAgain.ResponseBody) {
		t.Fatalf(
			"second click changed first click timestamp: first=%s second=%s",
			opened.ResponseBody,
			openedAgain.ResponseBody,
		)
	}
	target.Key[0] = 3
	reference := "private booking reference"
	reported, err := f.service.UpdateParticipation(
		ctx,
		*target,
		ParticipationInput{Action: "user_reported_confirmed", PrivateReference: &reference},
	)
	if err != nil {
		t.Fatal(err)
	}
	checkStatus(t, &reported, "READY", "2")
	var proposalState string
	if scanErr := f.gateway.QueryRow(
		ctx,
		`SELECT state FROM planning.route_proposal WHERE id = $1`,
		idString(f.proposal[:]),
	).Scan(&proposalState); scanErr != nil || proposalState != "invalidated" {
		t.Fatalf("proposal state=%q error=%v", proposalState, scanErr)
	}
	if len(reported.ResponseBody) == 0 || strings.Contains(string(reported.ResponseBody), reference) ||
		!json.Valid(reported.ResponseBody) {
		t.Fatal("invalid report response")
	}
	replay, err := f.service.UpdateParticipation(
		ctx,
		*target,
		ParticipationInput{Action: "user_reported_confirmed", PrivateReference: &reference},
	)
	if err != nil || !replay.Replayed || !sameJSON(replay.ResponseBody, reported.ResponseBody) {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
}

func assertClearAndExecution(
	ctx context.Context,
	t *testing.T,
	f *visitIntegrationFixture,
	target *VisitCommand,
) {
	t.Helper()
	target.Key[0] = 4
	_, staleErr := f.service.UpdateParticipation(ctx, *target, ParticipationInput{Action: "clear_user_report"})
	if staleErr == nil {
		t.Fatal("stale revision accepted")
	}
	var conflict *command.RevisionConflictError
	if !errors.As(staleErr, &conflict) || conflict.Current != 2 {
		t.Fatalf("unexpected conflict: %v", staleErr)
	}
	target.ExpectedRevision = 2
	cleared, err := f.service.UpdateParticipation(ctx, *target, ParticipationInput{Action: "clear_user_report"})
	if err != nil {
		t.Fatal(err)
	}
	checkStatus(t, &cleared, "READY", "3")
	target.ExpectedRevision, target.Key[0] = 3, 5
	completed, err := f.service.UpdateExecution(
		ctx,
		*target,
		ExecutionInput{Status: domain.ExecutionCompleted, ConfirmationKind: domain.ConfirmationUserReported},
	)
	if err != nil {
		t.Fatal(err)
	}
	checkStatus(t, &completed, "READY", "4")
	var revisionCount int
	if scanErr := f.gateway.QueryRow(
		ctx,
		`SELECT count(*) FROM planning.route_revision WHERE route_id = $1`,
		f.routeText,
	).Scan(&revisionCount); scanErr != nil || revisionCount != 4 {
		t.Fatalf("revision count=%d error=%v", revisionCount, scanErr)
	}
}

func assertProviderPrecedence(
	ctx context.Context,
	t *testing.T,
	f *visitIntegrationFixture,
	target *VisitCommand,
) {
	t.Helper()
	var source, record [16]byte
	for _, id := range [][]byte{source[:], record[:]} {
		fillRandomUUID(t, id)
	}
	if _, err := f.syncer.Exec(
		ctx,
		`INSERT INTO integration.source (
    id, source_key, name, access_mode, schema_version, is_enabled
) VALUES ($1, $2, 'Fixture provider', 'synthetic', '1', true)`,
		idString(source[:]),
		"test:"+idString(source[:]),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := f.syncer.Exec(ctx, `INSERT INTO integration.source_record (
    id, source_id, city, external_id, source_url, last_seen_at, data_mode
) VALUES ($1, $2, 'moscow', $3, 'https://example.invalid/fixture', $4, 'synthetic')`,
		idString(record[:]), idString(source[:]), idString(record[:]), f.now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.gateway.Exec(
		ctx,
		`UPDATE planning.participation
SET status = 'provider_confirmed', evidence_source = 'provider', provider_record_id = $3,
    updated_in_revision = 4 WHERE route_id = $1 AND visit_id = $2`,
		f.routeText,
		f.visitText,
		idString(record[:]),
	); err != nil {
		t.Fatal(err)
	}
	target.ExpectedRevision, target.Key[0] = 4, 6
	preserved, err := f.service.UpdateParticipation(ctx, *target, ParticipationInput{Action: "clear_user_report"})
	if err != nil {
		t.Fatal(err)
	}
	checkStatus(t, &preserved, "UNCHANGED", "4")
	var storedStatus, evidence string
	if err := f.gateway.QueryRow(ctx, `SELECT status, evidence_source FROM planning.participation
WHERE route_id = $1 AND visit_id = $2`, f.routeText, f.visitText).
		Scan(&storedStatus, &evidence); err != nil ||
		storedStatus != "provider_confirmed" || evidence != "provider" {
		t.Fatalf("provider state=%s/%s error=%v", storedStatus, evidence, err)
	}
}

func checkStatus(t *testing.T, result *command.Result, wantStatus, wantRevision string) {
	t.Helper()
	var body struct {
		Status   string `json:"status"`
		Revision string `json:"revision"`
	}
	if err := json.Unmarshal(
		result.ResponseBody,
		&body,
	); err != nil || body.Status != wantStatus ||
		body.Revision != wantRevision {
		t.Fatalf("body=%s error=%v", result.ResponseBody, err)
	}
}

func sameJSON(a, b []byte) bool {
	var first, second any
	return json.Unmarshal(a, &first) == nil && json.Unmarshal(b, &second) == nil && reflect.DeepEqual(first, second)
}
