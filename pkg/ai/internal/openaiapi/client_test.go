package openaiapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return New("acme", srv.URL, "secret-key", srv.Client())
}

func decode(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	return body
}

func TestEmbeddingsSendsRequestAndOrdersByIndex(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/embeddings" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret-key" {
			t.Errorf("authorization %q", got)
		}
		body := decode(t, r)
		if body["model"] != "openai/text-embedding-3-small" || body["dimensions"] != float64(384) ||
			body["encoding_format"] != "float" {
			t.Errorf("body %v", body)
		}
		if input, _ := body["input"].([]any); len(input) != 2 || input[0] != "a" || input[1] != "b" {
			t.Errorf("input %v", body["input"])
		}
		_, _ = io.WriteString(w, `{"data":[{"index":1,"embedding":[0.2,0.3]},{"index":0,"embedding":[0.0,0.1]}]}`)
	})
	got, err := c.Embeddings(context.Background(), "openai/text-embedding-3-small", []string{"a", "b"}, 384)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0][1] != 0.1 || got[1][0] != 0.2 {
		t.Fatalf("vectors %v", got)
	}
}

func TestEmbeddingsRejectsMismatchedData(t *testing.T) {
	for name, payload := range map[string]string{
		"missing":   `{"data":[{"index":0,"embedding":[0.1]}]}`,
		"duplicate": `{"data":[{"index":0,"embedding":[0.1]},{"index":0,"embedding":[0.2]}]}`,
		"range":     `{"data":[{"index":0,"embedding":[0.1]},{"index":5,"embedding":[0.2]}]}`,
		"empty":     `{"data":[{"index":0,"embedding":[0.1]},{"index":1,"embedding":[]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			c := serve(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, payload) })
			if _, err := c.Embeddings(context.Background(), "m", []string{"a", "b"}, 1); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestErrorsCarryStatusAndMessageWithoutKey(t *testing.T) {
	cases := map[string]struct {
		status  int
		body    string
		message string
	}{
		"json":  {http.StatusUnauthorized, `{"error":{"code":401,"message":"bad key"}}`, "bad key"},
		"html":  {http.StatusBadGateway, `<html>gateway</html>`, "Bad Gateway"},
		"empty": {http.StatusTooManyRequests, ``, "Too Many Requests"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			_, err := c.Embeddings(context.Background(), "m", []string{"a"}, 1)
			var statusErr *StatusError
			if !errors.As(err, &statusErr) || statusErr.Provider != "acme" || statusErr.Status != tc.status ||
				statusErr.Message != tc.message {
				t.Fatalf("error %#v", err)
			}
			if !strings.HasPrefix(err.Error(), "acme responded") {
				t.Fatalf("error %q", err)
			}
			if strings.Contains(err.Error(), "secret-key") {
				t.Fatalf("error leaks the key: %v", err)
			}
		})
	}
}

func TestEmbeddingsRejectsInvalidJSON(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `not json`) })
	if _, err := c.Embeddings(context.Background(), "m", []string{"a"}, 1); err == nil {
		t.Fatal("expected an error")
	}
}

func TestCompleteReturnsFirstChoice(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path %s", r.URL.Path)
		}
		body := decode(t, r)
		messages, _ := body["messages"].([]any)
		if body["model"] != "openai/gpt-4o-mini" || len(messages) != 2 {
			t.Errorf("body %v", body)
		}
		first, _ := messages[0].(map[string]any)
		second, _ := messages[1].(map[string]any)
		if first["role"] != "system" || first["content"] != "be brief" || second["role"] != "user" ||
			second["content"] != "hi" {
			t.Errorf("messages %v", messages)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`)
	})
	got, err := c.Complete(context.Background(), "openai/gpt-4o-mini", "be brief", "hi")
	if err != nil || got != "hello" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestCompleteOmitsEmptySystemMessage(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if messages, _ := decode(t, r)["messages"].([]any); len(messages) != 1 {
			t.Errorf("messages %v", messages)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	})
	if _, err := c.Complete(context.Background(), "m", "", "hi"); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteRejectsNoChoices(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"choices":[]}`) })
	if _, err := c.Complete(context.Background(), "m", "", "hi"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestGetSendsKeyAndDecodes(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/models" || r.URL.Query().Get("type") != "embedding" {
			t.Errorf("request %s %s", r.Method, r.URL)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret-key" {
			t.Errorf("authorization %q", got)
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"e/one"}]}`)
	})
	var resp struct {
		Data []Model `json:"data"`
	}
	if err := c.Get(
		context.Background(),
		"/models?type=embedding",
		&resp,
	); err != nil || len(resp.Data) != 1 ||
		resp.Data[0].ID != "e/one" {
		t.Fatalf("models %v, %v", resp.Data, err)
	}
}

func TestNewTrimsBaseURL(t *testing.T) {
	c := New("acme", "http://example.test/api/", "", nil)
	if c.baseURL != "http://example.test/api" {
		t.Fatalf("base url %q", c.baseURL)
	}
}
