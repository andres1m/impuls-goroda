package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/maxbot"
)

type recordingBot struct {
	calls  int
	update maxbot.Update
}

func (b *recordingBot) Handle(_ context.Context, update *maxbot.Update) error {
	b.calls++
	b.update = *update
	return nil
}

func TestBotWebhookRequiresSecretAndAcceptsSingleUpdate(t *testing.T) {
	checker := &fakeRuntime{webhookOK: false}
	bot := &recordingBot{}
	e := testEcho()
	for _, route := range NewBotRouter(checker, bot).Routes() {
		route.Register(context.Background(), e.Group("/api/v1"))
	}
	body := []byte(`{"update_type":"bot_started","timestamp":1,"user":{"user_id":42}}`)
	request := func() *http.Request {
		return httptest.NewRequest(http.MethodPost, "/api/v1/max/webhook", bytes.NewReader(body))
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, request())
	if rec.Code != http.StatusUnauthorized || bot.calls != 0 {
		t.Fatalf("unauthorized webhook: status=%d calls=%d", rec.Code, bot.calls)
	}
	checker.webhookOK = true
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, request())
	if rec.Code != http.StatusOK || bot.calls != 1 || bot.update.User.ID != 42 {
		t.Fatalf("valid webhook: status=%d calls=%d", rec.Code, bot.calls)
	}
	bad := httptest.NewRequest(http.MethodPost, "/api/v1/max/webhook", bytes.NewBufferString(`{} {}`))
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, bad)
	if rec.Code != http.StatusBadRequest || bot.calls != 1 {
		t.Fatalf("malformed webhook: status=%d calls=%d", rec.Code, bot.calls)
	}
}
