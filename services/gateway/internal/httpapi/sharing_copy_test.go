package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/app"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/auth"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/sharewire"
	"github.com/labstack/echo/v5"
)

type copyShareRuntime struct {
	*fakeRuntime
	called bool
}

func (*copyShareRuntime) ReadSharedRoute(context.Context, sharewire.Token) (sharewire.SharedRoute, error) {
	return sharewire.SharedRoute{}, nil
}
func (*copyShareRuntime) CreateShare(context.Context, app.ShareCommand, sharewire.CreateInput) (command.Result, error) {
	return command.Result{}, nil
}
func (*copyShareRuntime) RevokeShare(context.Context, app.ShareCommand) (command.Result, error) {
	return command.Result{}, nil
}
func (r *copyShareRuntime) CopySharedRoute(_ context.Context, _ domain.UserID, _ [16]byte,
	_ sharewire.Token, _ domain.RouteRevisionNumber, _ sharewire.CopyInput, _ string) (command.Result, error) {
	r.called = true
	return command.Result{}, &app.CopyRefusal{Status: "CONFLICT", Conflicts: []routewire.Conflict{{Code: "OBLIGATION_UNAVAILABLE", Message: "Visit is unavailable"}}}
}

func TestCopyShareReturnsConflictsWithoutDraft(t *testing.T) {
	runtime := &copyShareRuntime{fakeRuntime: &fakeRuntime{allowUser: true,
		pair: auth.SessionAccount{Account: domain.UserAccount{ID: domain.UserID{1}, Kind: domain.AccountMax}}}}
	e := testEcho()
	for _, route := range NewShareRouter(runtime, "example_bot").Routes() {
		route.Register(context.Background(), e.Group("/api/v1"))
	}
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/shared-routes/"+token+"/copy",
		bytes.NewBufferString(`{"origin":{"longitude":56,"latitude":58},"accepted_unknowns":[]}`))
	request.Header.Set(echo.HeaderAuthorization, "Bearer abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ")
	request.Header.Set("Idempotency-Key", "11111111-1111-4111-8111-111111111111")
	request.Header.Set("If-Match", `"1"`)
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity || !runtime.called {
		t.Fatalf("status=%d called=%v body=%s", response.Code, runtime.called, response.Body.String())
	}
	var body struct {
		Status    string               `json:"status"`
		Conflicts []routewire.Conflict `json:"conflicts"`
		Route     *json.RawMessage     `json:"route"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "CONFLICT" || len(body.Conflicts) != 1 || body.Route != nil {
		t.Fatalf("unexpected refusal: %+v", body)
	}
}
