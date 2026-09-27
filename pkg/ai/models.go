package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/andres1m/impuls-goroda/pkg/ai/openrouter"
)

const probeText = "проверка"

// Models holds the configured providers and the model currently selected for each kind.
type Models struct {
	cfg       Config
	client    *openrouter.Client
	embedding atomic.Pointer[string]
	text      atomic.Pointer[string]
}

func New(cfg Config) (*Models, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	httpClient := &http.Client{Timeout: cfg.OpenRouter.Timeout}
	m := &Models{cfg: cfg, client: openrouter.New(cfg.OpenRouter.BaseURL, cfg.OpenRouter.APIKey, httpClient)}
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

func (m *Models) Selected(kind Kind) string {
	if kind == KindText {
		return *m.text.Load()
	}
	return *m.embedding.Load()
}

// Available lists the models the kind's provider offers; a local provider offers none to choose.
func (m *Models) Available(ctx context.Context, kind Kind) ([]ModelInfo, error) {
	var list func(context.Context) ([]openrouter.Model, error)
	switch kind {
	case KindEmbedding:
		list = m.client.EmbeddingModels
	case KindText:
		list = m.client.TextModels
	default:
		return nil, ErrUnknownKind
	}
	if m.Provider(kind) != ProviderOpenRouter {
		return nil, nil
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
	if m.Provider(kind) != ProviderOpenRouter {
		return fmt.Errorf("provider %s has no selectable models: %w", m.Provider(kind), ErrNotImplemented)
	}
	if kind == KindEmbedding {
		if _, err := m.embedOpenRouter(ctx, model, []string{probeText}); err != nil {
			return err
		}
	}
	selected.Store(&model)
	return nil
}

func (m *Models) space(model string) Space {
	return Space{Key: m.cfg.Embedding.Provider + "/" + model, Version: "d" + strconv.Itoa(m.cfg.Embedding.Dimensions)}
}

func (m *Models) embedOpenRouter(ctx context.Context, model string, texts []string) ([][]float32, error) {
	if m.cfg.OpenRouter.APIKey == "" {
		return nil, ErrNotConfigured
	}
	vectors, err := m.client.Embeddings(ctx, model, texts, m.cfg.Embedding.Dimensions)
	if err != nil {
		return nil, err
	}
	for _, v := range vectors {
		if len(v) != m.cfg.Embedding.Dimensions {
			return nil, fmt.Errorf("%w: model %s returned %d, want %d", ErrDimensions, model, len(v), m.cfg.Embedding.Dimensions)
		}
	}
	return vectors, nil
}

type embedder struct{ m *Models }

func (e embedder) Space() Space { return e.m.space(e.m.Selected(KindEmbedding)) }

func (e embedder) Embed(ctx context.Context, texts []string) (Embedding, error) {
	model := e.m.Selected(KindEmbedding)
	out := Embedding{Space: e.m.space(model)}
	if e.m.cfg.Embedding.Provider == ProviderLocal {
		return out, fmt.Errorf("local embedding model %s: %w", model, ErrNotImplemented)
	}
	if len(texts) == 0 {
		return out, nil
	}
	vectors, err := e.m.embedOpenRouter(ctx, model, texts)
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
	if t.m.cfg.Text.Provider == ProviderLocal {
		return "", fmt.Errorf("local text model %s: %w", model, ErrNotImplemented)
	}
	if t.m.cfg.OpenRouter.APIKey == "" {
		return "", ErrNotConfigured
	}
	return t.m.client.Complete(ctx, model, p.System, p.User)
}
