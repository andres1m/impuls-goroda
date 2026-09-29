package ai

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func newAdmin(t *testing.T, key string) (*Admin, *Models) {
	t.Helper()
	f := newFakeProvider(t)
	m := mustNew(t, openRouterConfig(f.URL, key))
	return NewAdmin(m, AdminConfig{Enabled: true}, zap.NewNop()), m
}

func call(
	t *testing.T,
	h http.Handler,
	method, target, body string,
) (status int, data map[string]any, responseBody string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
	var payload map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &payload)
	return rec.Code, payload, rec.Body.String()
}

func TestAdminState(t *testing.T) {
	a, _ := newAdmin(t, "k")
	code, payload, _ := call(t, a.Handler(), http.MethodGet, "/api/state", "")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	embedding, _ := payload["embedding"].(map[string]any)
	space, _ := embedding["space"].(map[string]any)
	text, _ := payload["text"].(map[string]any)
	if embedding["provider"] != "openrouter" || embedding["model"] != "openai/text-embedding-3-small" ||
		space["key"] != "openrouter/openai/text-embedding-3-small" || space["version"] != "d384" ||
		text["provider"] != "openrouter" || text["model"] != "openai/gpt-4o-mini" {
		t.Fatalf("state %v", payload)
	}
	if embedding["api_key_set"] != true || text["api_key_set"] != true {
		t.Fatalf("api_key_set %v", payload)
	}
}

func TestAdminStateReportsMissingKeyPerKind(t *testing.T) {
	cfg := polzaConfig("", "")
	cfg.OpenRouter.APIKey = "k"
	cfg.Text.Provider = ProviderOpenRouter
	a := NewAdmin(mustNew(t, cfg), AdminConfig{}, zap.NewNop())
	_, payload, _ := call(t, a.Handler(), http.MethodGet, "/api/state", "")
	embedding, _ := payload["embedding"].(map[string]any)
	text, _ := payload["text"].(map[string]any)
	if embedding["provider"] != "polza" || embedding["api_key_set"] != false || text["api_key_set"] != true {
		t.Fatalf("state %v", payload)
	}
}

func TestAdminPage(t *testing.T) {
	a, _ := newAdmin(t, "k")
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") ||
		!strings.Contains(rec.Body.String(), "/api/selection") {
		t.Fatalf("page %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestAdminModels(t *testing.T) {
	a, _ := newAdmin(t, "")
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/models?kind=embedding", http.NoBody))
	var models []ModelInfo
	if err := json.Unmarshal(
		rec.Body.Bytes(),
		&models,
	); rec.Code != http.StatusOK || err != nil || len(models) != 1 ||
		models[0].ID != "e/one" {
		t.Fatalf("models %d %s", rec.Code, rec.Body.String())
	}
	if code, _, _ := call(t, a.Handler(), http.MethodGet, "/api/models?kind=image", ""); code != http.StatusBadRequest {
		t.Fatalf("unknown kind status %d", code)
	}
}

func TestAdminModelsOfLocalProviderAreEmptyList(t *testing.T) {
	cfg := openRouterConfig("", "")
	cfg.Embedding.Provider = ProviderLocal
	a := NewAdmin(mustNew(t, cfg), AdminConfig{}, zap.NewNop())
	if code, _, body := call(
		t,
		a.Handler(),
		http.MethodGet,
		"/api/models?kind=embedding",
		"",
	); code != http.StatusOK ||
		strings.TrimSpace(body) != "[]" {
		t.Fatalf("models %d %s", code, body)
	}
}

func TestAdminModelsProviderFailure(t *testing.T) {
	srv := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }),
	)
	t.Cleanup(srv.Close)
	a := NewAdmin(mustNew(t, openRouterConfig(srv.URL, "k")), AdminConfig{}, zap.NewNop())
	if code, payload, _ := call(
		t,
		a.Handler(),
		http.MethodGet,
		"/api/models?kind=text",
		"",
	); code != http.StatusBadGateway ||
		payload["error"] == nil {
		t.Fatalf("status %d %v", code, payload)
	}
}

func TestAdminSelection(t *testing.T) {
	a, m := newAdmin(t, "k")
	code, payload, _ := call(t, a.Handler(), http.MethodPut, "/api/selection", `{"kind":"embedding","model":"e/one"}`)
	if code != http.StatusOK || m.Selected(KindEmbedding) != "e/one" {
		t.Fatalf("status %d, selected %q", code, m.Selected(KindEmbedding))
	}
	if embedding, _ := payload["embedding"].(map[string]any); embedding["model"] != "e/one" {
		t.Fatalf("state %v", payload)
	}

	for name, tc := range map[string]struct {
		body string
		code int
	}{
		"wrong dimensions": {`{"kind":"embedding","model":"wide/model"}`, http.StatusUnprocessableEntity},
		"unknown kind":     {`{"kind":"image","model":"x"}`, http.StatusBadRequest},
		"blank model":      {`{"kind":"text","model":""}`, http.StatusBadRequest},
		"invalid json":     {`{`, http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			code, payload, _ := call(t, a.Handler(), http.MethodPut, "/api/selection", tc.body)
			if code != tc.code || payload["error"] == nil {
				t.Fatalf("status %d %v", code, payload)
			}
			if m.Selected(KindEmbedding) != "e/one" {
				t.Fatalf("selection changed to %q", m.Selected(KindEmbedding))
			}
		})
	}
}

func TestAdminRejectsOtherMethods(t *testing.T) {
	a, _ := newAdmin(t, "k")
	if code, _, _ := call(t, a.Handler(), http.MethodPost, "/api/state", ""); code != http.StatusMethodNotAllowed {
		t.Fatalf("status %d", code)
	}
}

func TestAdminListensOnLoopbackByDefault(t *testing.T) {
	a, _ := newAdmin(t, "k")
	if err := a.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })
	host, _, splitErr := net.SplitHostPort(a.Addr().String())
	if splitErr != nil || host != "127.0.0.1" {
		t.Fatalf("listening on %v", a.Addr())
	}
	if err := a.HealthCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	go func() { _ = a.Run(context.Background()) }()
	resp, err := http.Get("http://" + a.Addr().String() + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
