package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeOpenRouter answers embeddings with vectors whose length depends on the model:
// models named "wide/*" return 1536 values, all others the requested dimensions.
type fakeOpenRouter struct {
	*httptest.Server
	embedCalls atomic.Int32
}

func newFakeOpenRouter(t *testing.T) *fakeOpenRouter {
	t.Helper()
	f := &fakeOpenRouter{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/embeddings":
			f.embedCalls.Add(1)
			var req struct {
				Model      string   `json:"model"`
				Input      []string `json:"input"`
				Dimensions int      `json:"dimensions"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Model == "broken/model" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":{"message":"no such model"}}`)
				return
			}
			size := req.Dimensions
			if strings.HasPrefix(req.Model, "wide/") {
				size = 1536
			}
			var items []string
			for i := range req.Input {
				items = append(items, fmt.Sprintf(`{"index":%d,"embedding":%s}`, i, vector(size)))
			}
			_, _ = io.WriteString(w, `{"data":[`+strings.Join(items, ",")+`]}`)
		case "/chat/completions":
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"answer"}}]}`)
		case "/embeddings/models":
			_, _ = io.WriteString(w, `{"data":[{"id":"e/one","name":"One","context_length":512}]}`)
		case "/models":
			_, _ = io.WriteString(w, `{"data":[{"id":"t/one","name":"Text","context_length":8000}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func vector(size int) string {
	values := make([]string, size)
	for i := range values {
		values[i] = "0.01"
	}
	return "[" + strings.Join(values, ",") + "]"
}

func openRouterConfig(baseURL, key string) Config {
	return Config{
		OpenRouter: OpenRouterConfig{BaseURL: baseURL, APIKey: key},
		Embedding:  EmbeddingConfig{Provider: ProviderOpenRouter, Model: "openai/text-embedding-3-small", Dimensions: 384},
		Text:       TextConfig{Provider: ProviderOpenRouter, Model: "openai/gpt-4o-mini"},
	}
}

func mustNew(t *testing.T, cfg Config) *Models {
	t.Helper()
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestConfigValidate(t *testing.T) {
	valid := openRouterConfig("", "k")
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	for name, mutate := range map[string]func(*Config){
		"unknown embedding provider": func(c *Config) { c.Embedding.Provider = "magic" },
		"unknown text provider":      func(c *Config) { c.Text.Provider = "" },
		"empty embedding model":      func(c *Config) { c.Embedding.Model = " " },
		"empty text model":           func(c *Config) { c.Text.Model = "" },
		"zero dimensions":            func(c *Config) { c.Embedding.Dimensions = 0 },
		"negative timeout":           func(c *Config) { c.OpenRouter.Timeout = -1 },
		"admin port":                 func(c *Config) { c.Admin = AdminConfig{Enabled: true, Port: 70000} },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := openRouterConfig("", "k")
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected an error")
			}
			if _, err := New(cfg); err == nil {
				t.Fatal("New accepted an invalid config")
			}
		})
	}
}

func TestEmbedReturnsVectorsWithTheirSpace(t *testing.T) {
	f := newFakeOpenRouter(t)
	m := mustNew(t, openRouterConfig(f.URL, "k"))
	got, err := m.Embedder().Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	want := Space{Key: "openrouter/openai/text-embedding-3-small", Version: "d384"}
	if got.Space != want || len(got.Vectors) != 2 || len(got.Vectors[1]) != 384 {
		t.Fatalf("embedding %v with %d vectors", got.Space, len(got.Vectors))
	}
	if m.Embedder().Space() != want {
		t.Fatalf("space %v", m.Embedder().Space())
	}
}

func TestEmbedWithoutKeyIsNotConfigured(t *testing.T) {
	f := newFakeOpenRouter(t)
	m := mustNew(t, openRouterConfig(f.URL, ""))
	if _, err := m.Embedder().Embed(context.Background(), []string{"a"}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("error %v", err)
	}
	if _, err := m.Text().Complete(context.Background(), Prompt{User: "hi"}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("error %v", err)
	}
	if f.embedCalls.Load() != 0 {
		t.Fatal("request sent without a key")
	}
}

func TestEmbedRejectsUnexpectedDimensions(t *testing.T) {
	f := newFakeOpenRouter(t)
	cfg := openRouterConfig(f.URL, "k")
	cfg.Embedding.Model = "wide/model"
	m := mustNew(t, cfg)
	if _, err := m.Embedder().Embed(context.Background(), []string{"a"}); !errors.Is(err, ErrDimensions) {
		t.Fatalf("error %v", err)
	}
}

func TestLocalProviderIsNotImplemented(t *testing.T) {
	cfg := openRouterConfig("", "")
	cfg.Embedding.Provider, cfg.Text.Provider = ProviderLocal, ProviderLocal
	cfg.Embedding.Model = "multilingual-e5-small"
	m := mustNew(t, cfg)
	if _, err := m.Embedder().Embed(context.Background(), []string{"a"}); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("embed error %v", err)
	}
	if _, err := m.Text().Complete(context.Background(), Prompt{User: "hi"}); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("complete error %v", err)
	}
	if got := m.Embedder().Space(); got != (Space{Key: "local/multilingual-e5-small", Version: "d384"}) {
		t.Fatalf("space %v", got)
	}
	if models, err := m.Available(context.Background(), KindEmbedding); err != nil || len(models) != 0 {
		t.Fatalf("available %v, %v", models, err)
	}
	if err := m.Select(context.Background(), KindEmbedding, "other"); err == nil {
		t.Fatal("local provider accepted a selection")
	}
}

func TestCompleteUsesSelectedModel(t *testing.T) {
	f := newFakeOpenRouter(t)
	m := mustNew(t, openRouterConfig(f.URL, "k"))
	got, err := m.Text().Complete(context.Background(), Prompt{System: "s", User: "u"})
	if err != nil || got != "answer" {
		t.Fatalf("got %q, %v", got, err)
	}
	if err := m.Select(context.Background(), KindText, "t/one"); err != nil {
		t.Fatal(err)
	}
	if m.Text().Model() != "t/one" || m.Selected(KindText) != "t/one" {
		t.Fatalf("text model %q", m.Text().Model())
	}
}

func TestSelectEmbeddingProbesDimensions(t *testing.T) {
	f := newFakeOpenRouter(t)
	m := mustNew(t, openRouterConfig(f.URL, "k"))
	before := m.Embedder().Space()

	for name, model := range map[string]string{"wide": "wide/model", "unavailable": "broken/model", "blank": " "} {
		t.Run(name, func(t *testing.T) {
			if err := m.Select(context.Background(), KindEmbedding, model); err == nil {
				t.Fatal("expected a refusal")
			}
			if m.Embedder().Space() != before {
				t.Fatalf("space changed to %v", m.Embedder().Space())
			}
		})
	}

	if err := m.Select(context.Background(), KindEmbedding, "e/one"); err != nil {
		t.Fatal(err)
	}
	if got := m.Embedder().Space(); got != (Space{Key: "openrouter/e/one", Version: "d384"}) {
		t.Fatalf("space %v", got)
	}
}

func TestSelectWithoutKeyIsRefused(t *testing.T) {
	f := newFakeOpenRouter(t)
	m := mustNew(t, openRouterConfig(f.URL, ""))
	if err := m.Select(context.Background(), KindEmbedding, "e/one"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("error %v", err)
	}
}

func TestSelectUnknownKind(t *testing.T) {
	m := mustNew(t, openRouterConfig("", "k"))
	if err := m.Select(context.Background(), Kind("image"), "x"); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("error %v", err)
	}
	if _, err := m.Available(context.Background(), Kind("image")); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("error %v", err)
	}
}

func TestAvailableListsProviderModels(t *testing.T) {
	f := newFakeOpenRouter(t)
	m := mustNew(t, openRouterConfig(f.URL, ""))
	embedding, err := m.Available(context.Background(), KindEmbedding)
	if err != nil || len(embedding) != 1 || embedding[0] != (ModelInfo{ID: "e/one", Name: "One", ContextLength: 512}) {
		t.Fatalf("embedding models %v, %v", embedding, err)
	}
	text, err := m.Available(context.Background(), KindText)
	if err != nil || len(text) != 1 || text[0].ID != "t/one" {
		t.Fatalf("text models %v, %v", text, err)
	}
}
