// Package openrouter is the OpenRouter provider: the shared OpenAI-compatible API plus
// OpenRouter's model catalogs.
package openrouter

import (
	"context"
	"net/http"

	"github.com/andres1m/impuls-goroda/pkg/ai/internal/openaiapi"
)

const DefaultBaseURL = "https://openrouter.ai/api/v1"

type Client struct {
	*openaiapi.Client
}

func New(baseURL, apiKey string, httpClient *http.Client) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{openaiapi.New("openrouter", baseURL, apiKey, httpClient)}
}

func (c *Client) EmbeddingModels(ctx context.Context) ([]openaiapi.Model, error) {
	return c.models(ctx, "/embeddings/models")
}

func (c *Client) TextModels(ctx context.Context) ([]openaiapi.Model, error) {
	return c.models(ctx, "/models")
}

func (c *Client) models(ctx context.Context, path string) ([]openaiapi.Model, error) {
	var resp struct {
		Data []openaiapi.Model `json:"data"`
	}
	if err := c.Get(ctx, path, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}
