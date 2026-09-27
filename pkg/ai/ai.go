// Package ai gives services embedding and text models behind one interface. The provider
// (a hosted API or a local model) is fixed by configuration; the hosted model can be
// switched at run time from the local admin page.
package ai

import (
	"context"
	"errors"
)

var (
	ErrNotImplemented = errors.New("model provider is not implemented")
	ErrNotConfigured  = errors.New("model provider is not configured")
	ErrDimensions     = errors.New("embedding has unexpected dimensions")
	ErrUnknownKind    = errors.New("unknown model kind")
)

// Space identifies vectors that are comparable: produced by one model at one size.
// Vectors of different spaces must never be compared.
type Space struct {
	Key     string
	Version string
}

type Embedding struct {
	Space   Space
	Vectors [][]float32
}

type Embedder interface {
	// Embed returns one vector per text, tagged with the space of the model that produced them.
	Embed(ctx context.Context, texts []string) (Embedding, error)
	// Space is the space of the currently selected model.
	Space() Space
}

type Prompt struct {
	System string
	User   string
}

type TextModel interface {
	Complete(ctx context.Context, p Prompt) (string, error)
	Model() string
}

type Kind string

const (
	KindEmbedding Kind = "embedding"
	KindText      Kind = "text"
)

type ModelInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextLength int    `json:"context_length"`
}
