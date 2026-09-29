// Package openaiapi is a minimal client of the OpenAI-compatible HTTP API that hosted model
// providers share: embeddings and chat completions. Model catalogs differ per provider.
package openaiapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// A provider error page can be large; only its beginning is worth reading.
const maxErrorBody = 64 << 10

type Client struct {
	provider string
	baseURL  string
	apiKey   string
	http     *http.Client
}

// New names the provider in errors; the key is sent only as a bearer token.
func New(provider, baseURL, apiKey string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{provider: provider, baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, http: httpClient}
}

type StatusError struct {
	Provider string
	Status   int
	Message  string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s responded %d: %s", e.Provider, e.Status, e.Message)
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
		return nil, fmt.Errorf("%s returned %d embeddings for %d inputs", c.provider, len(resp.Data), len(input))
	}
	out := make([][]float32, len(input))
	for _, d := range resp.Data {
		if d.Index < 0 || d.Index >= len(out) || out[d.Index] != nil {
			return nil, fmt.Errorf("%s returned an embedding with unexpected index %d", c.provider, d.Index)
		}
		if len(d.Embedding) == 0 {
			return nil, fmt.Errorf("%s returned an empty embedding at index %d", c.provider, d.Index)
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
	const initialMessageCapacity = 2
	messages := make([]message, 0, initialMessageCapacity)
	if system != "" {
		messages = append(messages, message{Role: "system", Content: system})
	}
	messages = append(messages, message{Role: "user", Content: user})
	var resp completionResponse
	if err := c.do(
		ctx,
		http.MethodPost,
		"/chat/completions",
		completionRequest{Model: model, Messages: messages},
		&resp,
	); err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("%s returned no completion choices", c.provider)
	}
	return resp.Choices[0].Message.Content, nil
}

func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode %s request: %w", c.provider, err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("create %s request: %w", c.provider, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s request %s: %w", c.provider, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c.statusError(resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s response %s: %w", c.provider, path, err)
	}
	return nil
}

func (c *Client) statusError(resp *http.Response) error {
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	message := http.StatusText(resp.StatusCode)
	if readErr == nil && json.Unmarshal(data, &payload) == nil && payload.Error.Message != "" {
		message = payload.Error.Message
	}
	return &StatusError{Provider: c.provider, Status: resp.StatusCode, Message: message}
}
