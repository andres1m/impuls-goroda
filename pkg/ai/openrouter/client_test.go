package openrouter

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andres1m/impuls-goroda/pkg/ai/internal/openaiapi"
)

func TestModelLists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/embeddings/models":
			_, _ = io.WriteString(w, `{"data":[{"id":"e/one","name":"One","context_length":512}]}`)
		case "/models":
			_, _ = io.WriteString(w, `{"data":[{"id":"t/one","name":"Text","context_length":8000}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "k", srv.Client())
	embedding, err := c.EmbeddingModels(context.Background())
	if err != nil || len(embedding) != 1 ||
		embedding[0] != (openaiapi.Model{ID: "e/one", Name: "One", ContextLength: 512}) {
		t.Fatalf("embedding models %v, %v", embedding, err)
	}
	text, err := c.TextModels(context.Background())
	if err != nil || len(text) != 1 || text[0].ID != "t/one" {
		t.Fatalf("text models %v, %v", text, err)
	}
}

func TestErrorsNameOpenRouter(t *testing.T) {
	srv := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }),
	)
	t.Cleanup(srv.Close)
	_, err := New(srv.URL, "k", srv.Client()).Embeddings(context.Background(), "m", []string{"a"}, 1)
	if err == nil || err.Error() != "openrouter responded 401: Unauthorized" {
		t.Fatalf("error %v", err)
	}
}
