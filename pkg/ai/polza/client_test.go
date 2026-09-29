package polza

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andres1m/impuls-goroda/pkg/ai/internal/openaiapi"
)

func serve(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return New(srv.URL, "k", srv.Client())
}

func TestModelListsFilterByTypeAndReadProviderContext(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		switch r.URL.Query().Get("type") {
		case "embedding":
			_, _ = io.WriteString(
				w,
				`{"object":"list","data":[{"id":"openai/text-embedding-3-small","name":"Small","type":"embedding","top_provider":{"context_length":8192}}]}`,
			)
		case "chat":
			_, _ = io.WriteString(
				w,
				`{"object":"list","data":[{"id":"openai/gpt-4o-mini","name":"Mini","type":"chat","top_provider":{"context_length":128000}}]}`,
			)
		default:
			t.Errorf("type %q", r.URL.Query().Get("type"))
		}
	})
	embedding, err := c.EmbeddingModels(context.Background())
	want := openaiapi.Model{ID: "openai/text-embedding-3-small", Name: "Small", ContextLength: 8192}
	if err != nil || len(embedding) != 1 || embedding[0] != want {
		t.Fatalf("embedding models %v, %v", embedding, err)
	}
	text, err := c.TextModels(context.Background())
	if err != nil || len(text) != 1 ||
		text[0] != (openaiapi.Model{ID: "openai/gpt-4o-mini", Name: "Mini", ContextLength: 128000}) {
		t.Fatalf("text models %v, %v", text, err)
	}
}

func TestModelWithoutProviderHasNoContextLength(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"id":"yandex/text-search-doc","name":"Doc","top_provider":null}]}`)
	})
	models, err := c.EmbeddingModels(context.Background())
	if err != nil || len(models) != 1 || models[0] != (openaiapi.Model{ID: "yandex/text-search-doc", Name: "Doc"}) {
		t.Fatalf("models %v, %v", models, err)
	}
}

func TestEmbeddingsUseSharedAPIAndNamePolza(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/embeddings" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"code":"UNAUTHORIZED","message":"bad key","trace_id":"x"}}`)
	})
	_, err := c.Embeddings(context.Background(), "m", []string{"a"}, 384)
	if err == nil || err.Error() != "polza responded 401: bad key" {
		t.Fatalf("error %v", err)
	}
}
