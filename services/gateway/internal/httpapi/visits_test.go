package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/app"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/auth"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/labstack/echo/v5"
)

type fakeVisitRuntime struct {
	*fakeRuntime
	result        command.Result
	err           error
	target        app.VisitCommand
	participation app.ParticipationInput
	execution     app.ExecutionInput
}

func (f *fakeVisitRuntime) UpdateParticipation(_ context.Context, target app.VisitCommand, input app.ParticipationInput) (command.Result, error) {
	f.target, f.participation = target, input
	return f.result, f.err
}

func (f *fakeVisitRuntime) UpdateExecution(_ context.Context, target app.VisitCommand, input app.ExecutionInput) (command.Result, error) {
	f.target, f.execution = target, input
	return f.result, f.err
}

func TestVisitRoutesUseAuthenticatedCommandContract(t *testing.T) {
	routeID := domain.RouteID{1}
	visitID := domain.VisitID{2}
	revision := domain.RouteRevisionNumber(3)
	userID := domain.UserID{4}
	runtime := &fakeVisitRuntime{
		fakeRuntime: &fakeRuntime{
			allowUser: true,
			pair:      auth.SessionAccount{Account: domain.UserAccount{ID: userID, Kind: domain.AccountMax}},
		},
		result: command.Result{HTTPStatus: 200, ResponseBody: []byte(`{"status":"READY","route_id":"01000000-0000-0000-0000-000000000000","revision":"3","participation":{"visit_id":"02000000-0000-0000-0000-000000000000","status":"action_required","evidence":"none","updated_in_revision":"3","updated_at":"2026-09-26T12:00:00Z"}}`), RouteID: &routeID, ResultingRevision: &revision},
	}
	e := testEcho()
	for _, route := range NewVisitRouter(runtime).Routes() {
		route.Register(context.Background(), e.Group("/api/v1"))
	}
	path := "/api/v1/routes/01000000-0000-0000-0000-000000000000/visits/02000000-0000-0000-0000-000000000000/participation"
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"action":"external_link_opened"}`))
	request.Header.Set(echo.HeaderAuthorization, "Bearer abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ")
	request.Header.Set("Idempotency-Key", "11111111-1111-4111-8111-111111111111")
	request.Header.Set("If-Match", `"3"`)
	request.Header.Set(echo.HeaderXRequestID, "request-1")
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	if response.Code != 200 || response.Header().Get("ETag") != `"3"` {
		t.Fatalf("status=%d ETag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body.String())
	}
	if runtime.target.RouteID != routeID || runtime.target.VisitID != visitID || runtime.target.ActorID != userID || runtime.target.ExpectedRevision != 3 || runtime.participation.Action != "external_link_opened" {
		t.Fatalf("target=%+v input=%+v", runtime.target, runtime.participation)
	}
	var body struct {
		RequestID string `json:"request_id"`
		Revision  string `json:"revision"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.RequestID != "request-1" || body.Revision != "3" {
		t.Fatalf("body=%s error=%v", response.Body.String(), err)
	}
	request = httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"action":"external_link_opened"}`))
	response = httptest.NewRecorder()
	e.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth status=%d", response.Code)
	}
}

func TestVisitRouteRejectsUnknownFields(t *testing.T) {
	runtime := &fakeVisitRuntime{fakeRuntime: &fakeRuntime{
		allowUser: true, pair: auth.SessionAccount{Account: domain.UserAccount{ID: domain.UserID{4}, Kind: domain.AccountMax}},
	}}
	e := testEcho()
	for _, route := range NewVisitRouter(runtime).Routes() {
		route.Register(context.Background(), e.Group("/api/v1"))
	}
	path := "/api/v1/routes/01000000-0000-0000-0000-000000000000/visits/02000000-0000-0000-0000-000000000000/execution"
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"status":"completed","confirmation_kind":"user_reported","provider_record_id":"secret"}`))
	request.Header.Set(echo.HeaderAuthorization, "Bearer abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ")
	request.Header.Set("Idempotency-Key", "11111111-1111-4111-8111-111111111111")
	request.Header.Set("If-Match", `"3"`)
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestExecutionRouteReturnsUserReportedState(t *testing.T) {
	revision := domain.RouteRevisionNumber(4)
	runtime := &fakeVisitRuntime{
		fakeRuntime: &fakeRuntime{allowUser: true, pair: auth.SessionAccount{Account: domain.UserAccount{ID: domain.UserID{4}, Kind: domain.AccountMax}}},
		result: command.Result{HTTPStatus: 200, ResultingRevision: &revision,
			ResponseBody: []byte(`{"status":"READY","route_id":"01000000-0000-0000-0000-000000000000","revision":"4","execution":{"visit_id":"02000000-0000-0000-0000-000000000000","status":"completed","confirmation_kind":"user_reported","updated_in_revision":"4","updated_at":"2026-09-26T12:00:00Z"}}`)},
	}
	e := testEcho()
	for _, route := range NewVisitRouter(runtime).Routes() {
		route.Register(context.Background(), e.Group("/api/v1"))
	}
	path := "/api/v1/routes/01000000-0000-0000-0000-000000000000/visits/02000000-0000-0000-0000-000000000000/execution"
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"status":"completed","confirmation_kind":"user_reported"}`))
	request.Header.Set(echo.HeaderAuthorization, "Bearer abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ")
	request.Header.Set("Idempotency-Key", "22222222-2222-4222-8222-222222222222")
	request.Header.Set("If-Match", `"3"`)
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	if response.Code != 200 || response.Header().Get("ETag") != `"4"` || runtime.execution.Status != domain.ExecutionCompleted || runtime.execution.ConfirmationKind != domain.ConfirmationUserReported {
		t.Fatalf("status=%d ETag=%q input=%+v body=%s", response.Code, response.Header().Get("ETag"), runtime.execution, response.Body.String())
	}
}
