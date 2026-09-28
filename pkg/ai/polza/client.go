// Package polza is the Polza AI provider: the shared OpenAI-compatible API plus Polza's
// model catalog, which is one list filtered by model type.
package polza

import (
	"context"
	"net/http"

	"github.com/andres1m/impuls-goroda/pkg/ai/internal/openaiapi"
)

const DefaultBaseURL = "https://polza.ai/api/v1"

type Client struct {
	*openaiapi.Client
}

func New(baseURL, apiKey string, httpClient *http.Client) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{openaiapi.New("polza", baseURL, apiKey, httpClient)}
}

func (c *Client) EmbeddingModels(ctx context.Context) ([]openaiapi.Model, error) {
	return c.models(ctx, "embedding")
}

func (c *Client) TextModels(ctx context.Context) ([]openaiapi.Model, error) {
	return c.models(ctx, "chat")
}

type catalogModel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	TopProvider *struct {
		ContextLength int `json:"context_length"`
	} `json:"top_provider"`
}

func (c *Client) models(ctx context.Context, modelType string) ([]openaiapi.Model, error) {
	var resp struct {
		Data []catalogModel `json:"data"`
	}
	if err := c.Get(ctx, "/models?type="+modelType, &resp); err != nil {
		return nil, err
	}
	out := make([]openaiapi.Model, len(resp.Data))
	for i, m := range resp.Data {
		out[i] = openaiapi.Model{ID: m.ID, Name: m.Name}
		if m.TopProvider != nil {
			out[i].ContextLength = m.TopProvider.ContextLength
		}
	}
	return out, nil
}
