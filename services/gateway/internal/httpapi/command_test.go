package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"github.com/labstack/echo/v5"
)

func TestMutationHeaders(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Header.Set("Idempotency-Key", "11111111-1111-4111-8111-111111111111")
	request.Header.Set("If-Match", `"7"`)
	key, err := ParseIdempotencyKey(request)
	if err != nil || key == ([16]byte{}) {
		t.Fatalf("key=%x error=%v", key, err)
	}
	revision, err := ParseIfMatch(request)
	if err != nil || revision != 7 {
		t.Fatalf("revision=%d error=%v", revision, err)
	}
	for _, value := range []string{"", "11111111111141118111111111111111", "11111111-1111-4111-8111-11111111111g"} {
		request.Header.Set("Idempotency-Key", value)
		if _, err := ParseIdempotencyKey(request); err == nil {
			t.Fatalf("invalid key %q accepted", value)
		}
	}
	request.Header.Set("Idempotency-Key", "11111111-1111-4111-8111-111111111111")
	request.Header.Add("Idempotency-Key", "22222222-2222-4222-8222-222222222222")
	if _, err := ParseIdempotencyKey(request); err == nil {
		t.Fatal("duplicate key accepted")
	}
	for _, value := range []string{`7`, `"0"`, `"07"`, `"9223372036854775808"`, `"7", "8"`} {
		request.Header.Set("If-Match", value)
		if _, err := ParseIfMatch(request); err == nil {
			t.Fatalf("invalid If-Match %q accepted", value)
		}
	}
	request.Header.Set("If-Match", `"7"`)
	request.Header.Add("If-Match", `"8"`)
	if _, err := ParseIfMatch(request); err == nil {
		t.Fatal("duplicate If-Match accepted")
	}
}

func TestRevisionConflictHTTPResponse(t *testing.T) {
	e := testEcho()
	e.POST("/command", func(*echo.Context) error {
		return MapCommandError(&command.RevisionConflict{Current: domain.RouteRevisionNumber(8)})
	})
	request := httptest.NewRequest(http.MethodPost, "/command", nil)
	request.Header.Set("X-Request-ID", "request-1")
	recorder := httptest.NewRecorder()
	e.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || recorder.Header().Get("ETag") != `"8"` {
		t.Fatalf("status=%d ETag=%q", recorder.Code, recorder.Header().Get("ETag"))
	}
	var response errorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Code != "REVISION_CONFLICT" || response.CurrentRevision != "8" || response.RequestID != "request-1" {
		t.Fatalf("response=%+v", response)
	}
	var mapped *Error
	if !errors.As(MapCommandError(command.ErrIdempotencyKeyReused), &mapped) || mapped.Status != http.StatusConflict || mapped.Code != "IDEMPOTENCY_KEY_REUSED" {
		t.Fatalf("reused key mapping=%+v", mapped)
	}
	if !errors.As(MapCommandError(postgres.ErrNotFound), &mapped) || mapped.Status != http.StatusNotFound {
		t.Fatalf("missing route mapping=%+v", mapped)
	}
	if !errors.As(MapCommandError(errors.New("database offline")), &mapped) || mapped.Status != http.StatusServiceUnavailable || !mapped.Retryable {
		t.Fatalf("database failure mapping=%+v", mapped)
	}
}
