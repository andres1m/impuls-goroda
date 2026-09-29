package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/andres1m/impuls-goroda/pkg/ai/internal/openaiapi"
	"github.com/andres1m/impuls-goroda/pkg/ai/openrouter"
	"github.com/andres1m/impuls-goroda/pkg/ai/polza"
)

const probeText = "проверка"

type hostedClient interface {
	Embeddings(ctx context.Context, model string, input []string, dimensions int) ([][]float32, error)
	Complete(ctx context.Context, model, system, user string) (string, error)
	EmbeddingModels(ctx context.Context) ([]openaiapi.Model, error)
	TextModels(ctx context.Context) ([]openaiapi.Model, error)
}

// backend serves one model kind; a local provider has no hosted client.
type backend struct {
	client hostedClient
	keySet bool
}

func newBackend(cfg *Config, provider string) backend {
	switch provider {
	case ProviderOpenRouter:
		c := cfg.OpenRouter
		return backend{
			client: openrouter.New(c.BaseURL, c.APIKey, &http.Client{Timeout: c.Timeout}),
			keySet: c.APIKey != "",
		}
	case ProviderPolza:
		c := cfg.Polza
		return backend{client: polza.New(c.BaseURL, c.APIKey, &http.Client{Timeout: c.Timeout}), keySet: c.APIKey != ""}
	default:
		return backend{}
	}
}

// Models holds the configured providers and the model currently selected for each kind.
type Models struct {
	cfg              Config
	embeddingBackend backend
	textBackend      backend
	embedding        atomic.Pointer[string]
	text             atomic.Pointer[string]
}

//nolint:gocritic // public constructor stores a configuration snapshot
func New(cfg Config) (*Models, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	m := &Models{
		cfg:              cfg,
		embeddingBackend: newBackend(&cfg, cfg.Embedding.Provider),
		textBackend:      newBackend(&cfg, cfg.Text.Provider),
	}
	embedding, text := strings.TrimSpace(cfg.Embedding.Model), strings.TrimSpace(cfg.Text.Model)
	m.embedding.Store(&embedding)
	m.text.Store(&text)
	return m, nil
}

func (m *Models) Embedder() Embedder { return embedder{m} }

func (m *Models) Text() TextModel { return textModel{m} }

func (m *Models) Provider(kind Kind) string {
	if kind == KindText {
		return m.cfg.Text.Provider
	}
	return m.cfg.Embedding.Provider
}

func (m *Models) backendOf(kind Kind) backend {
	if kind == KindText {
		return m.textBackend
	}
	return m.embeddingBackend
}

func (m *Models) keyMissing(kind Kind) bool {
	b := m.backendOf(kind)
	return b.client != nil && !b.keySet
}

func (m *Models) Selected(kind Kind) string {
	if kind == KindText {
		return *m.text.Load()
	}
	return *m.embedding.Load()
}

// Available lists the models the kind's provider offers; a local provider offers none to choose.
func (m *Models) Available(ctx context.Context, kind Kind) ([]ModelInfo, error) {
	if kind != KindEmbedding && kind != KindText {
		return nil, ErrUnknownKind
	}
	client := m.backendOf(kind).client
	if client == nil {
		return nil, nil
	}
	list := client.EmbeddingModels
	if kind == KindText {
		list = client.TextModels
	}
	models, err := list(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ModelInfo, len(models))
	for i, model := range models {
		out[i] = ModelInfo(model)
	}
	return out, nil
}

// Select switches the hosted model of a kind. An embedding model is accepted only after it
// returned a vector of the configured size, so a wrong choice cannot corrupt stored vectors.
func (m *Models) Select(ctx context.Context, kind Kind, model string) error {
	model = strings.TrimSpace(model)
	var selected *atomic.Pointer[string]
	switch kind {
	case KindEmbedding:
		selected = &m.embedding
	case KindText:
		selected = &m.text
	default:
		return ErrUnknownKind
	}
	if model == "" {
		return errors.New("model is required")
	}
	if m.backendOf(kind).client == nil {
		return fmt.Errorf("provider %s has no selectable models: %w", m.Provider(kind), ErrNotImplemented)
	}
	if kind == KindEmbedding {
		if _, err := m.embedHosted(ctx, model, []string{probeText}); err != nil {
			return err
		}
	}
	selected.Store(&model)
	return nil
}

func (m *Models) space(model string) Space {
	return Space{Key: m.cfg.Embedding.Provider + "/" + model, Version: "d" + strconv.Itoa(m.cfg.Embedding.Dimensions)}
}

func (m *Models) embedHosted(ctx context.Context, model string, texts []string) ([][]float32, error) {
	if !m.embeddingBackend.keySet {
		return nil, ErrNotConfigured
	}
	vectors, err := m.embeddingBackend.client.Embeddings(ctx, model, texts, m.cfg.Embedding.Dimensions)
	if err != nil {
		return nil, err
	}
	for _, v := range vectors {
		if len(v) != m.cfg.Embedding.Dimensions {
			return nil, fmt.Errorf(
				"%w: model %s returned %d, want %d",
				ErrDimensions,
				model,
				len(v),
				m.cfg.Embedding.Dimensions,
			)
		}
	}
	return vectors, nil
}

type embedder struct{ m *Models }

func (e embedder) Space() Space { return e.m.space(e.m.Selected(KindEmbedding)) }

func (e embedder) Embed(ctx context.Context, texts []string) (Embedding, error) {
	model := e.m.Selected(KindEmbedding)
	out := Embedding{Space: e.m.space(model)}
	if e.m.embeddingBackend.client == nil {
		return out, fmt.Errorf("local embedding model %s: %w", model, ErrNotImplemented)
	}
	if len(texts) == 0 {
		return out, nil
	}
	vectors, err := e.m.embedHosted(ctx, model, texts)
	if err != nil {
		return out, err
	}
	out.Vectors = vectors
	return out, nil
}

type textModel struct{ m *Models }

func (t textModel) Model() string { return t.m.Selected(KindText) }

func (t textModel) Complete(ctx context.Context, p Prompt) (string, error) {
	model := t.Model()
	if t.m.textBackend.client == nil {
		return "", fmt.Errorf("local text model %s: %w", model, ErrNotImplemented)
	}
	if !t.m.textBackend.keySet {
		return "", ErrNotConfigured
	}
	return t.m.textBackend.client.Complete(ctx, model, p.System, p.User)
}
