// Package openrouter is a minimal client of the OpenRouter HTTP API: embeddings, chat
// completions and the model catalogs.
package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const DefaultBaseURL = "https://openrouter.ai/api/v1"

// A provider error page can be large; only its beginning is worth reading.
const maxErrorBody = 64 << 10

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func New(baseURL, apiKey string, httpClient *http.Client) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, http: httpClient}
}

type StatusError struct {
	Status  int
	Message string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("openrouter responded %d: %s", e.Status, e.Message)
}

type Model struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextLength int    `json:"context_length"`
}

type embeddingRequest struct {
	Model          string   `json:"model"`
	Input          []string `json:"input"`
	Dimensions     int      `json:"dimensions"`
	EncodingFormat string   `json:"encoding_format"`
}

type embeddingResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embeddings returns one vector per input, in input order.
func (c *Client) Embeddings(ctx context.Context, model string, input []string, dimensions int) ([][]float32, error) {
	var resp embeddingResponse
	req := embeddingRequest{Model: model, Input: input, Dimensions: dimensions, EncodingFormat: "float"}
	if err := c.do(ctx, http.MethodPost, "/embeddings", req, &resp); err != nil {
		return nil, err
	}
	if len(resp.Data) != len(input) {
		return nil, fmt.Errorf("openrouter returned %d embeddings for %d inputs", len(resp.Data), len(input))
	}
	out := make([][]float32, len(input))
	for _, d := range resp.Data {
		if d.Index < 0 || d.Index >= len(out) || out[d.Index] != nil {
			return nil, fmt.Errorf("openrouter returned an embedding with unexpected index %d", d.Index)
		}
		if len(d.Embedding) == 0 {
			return nil, fmt.Errorf("openrouter returned an empty embedding at index %d", d.Index)
		}
		out[d.Index] = d.Embedding
	}
	return out, nil
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type completionRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
}

type completionResponse struct {
	Choices []struct {
		Message message `json:"message"`
	} `json:"choices"`
}

func (c *Client) Complete(ctx context.Context, model, system, user string) (string, error) {
	messages := make([]message, 0, 2)
	if system != "" {
		messages = append(messages, message{Role: "system", Content: system})
	}
	messages = append(messages, message{Role: "user", Content: user})
	var resp completionResponse
	if err := c.do(ctx, http.MethodPost, "/chat/completions", completionRequest{Model: model, Messages: messages}, &resp); err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("openrouter returned no completion choices")
	}
	return resp.Choices[0].Message.Content, nil
}

func (c *Client) EmbeddingModels(ctx context.Context) ([]Model, error) {
	return c.models(ctx, "/embeddings/models")
}

func (c *Client) TextModels(ctx context.Context) ([]Model, error) {
	return c.models(ctx, "/models")
}

func (c *Client) models(ctx context.Context, path string) ([]Model, error) {
	var resp struct {
		Data []Model `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode openrouter request: %w", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("create openrouter request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("openrouter request %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return statusError(resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode openrouter response %s: %w", path, err)
	}
	return nil
}

func statusError(resp *http.Response) error {
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	message := http.StatusText(resp.StatusCode)
	if json.Unmarshal(data, &payload) == nil && payload.Error.Message != "" {
		message = payload.Error.Message
	}
	return &StatusError{Status: resp.StatusCode, Message: message}
}
