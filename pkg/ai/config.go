package ai

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	ProviderOpenRouter = "openrouter"
	ProviderLocal      = "local"
)

type Config struct {
	OpenRouter OpenRouterConfig `yaml:"openrouter"`
	Embedding  EmbeddingConfig  `yaml:"embedding"`
	Text       TextConfig       `yaml:"text"`
	Admin      AdminConfig      `yaml:"admin"`
}

type OpenRouterConfig struct {
	BaseURL string `yaml:"base-url"`
	// An empty key keeps the service running; hosted models then answer ErrNotConfigured.
	APIKey  string        `yaml:"api-key"`
	Timeout time.Duration `yaml:"timeout"`
}

type EmbeddingConfig struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	// Must equal the size of the stored vectors; hosted models are asked for exactly this size.
	Dimensions int `yaml:"dimensions"`
}

type TextConfig struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
}

type AdminConfig struct {
	Enabled bool `yaml:"enabled"`
	// Empty means 127.0.0.1: the page changes models, so it is not exposed beyond the machine.
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

func (c Config) Validate() error {
	if err := validProvider(c.Embedding.Provider, c.Embedding.Model); err != nil {
		return fmt.Errorf("embedding: %w", err)
	}
	if c.Embedding.Dimensions <= 0 {
		return errors.New("embedding: dimensions must be positive")
	}
	if err := validProvider(c.Text.Provider, c.Text.Model); err != nil {
		return fmt.Errorf("text: %w", err)
	}
	if c.OpenRouter.Timeout < 0 {
		return errors.New("openrouter: timeout must not be negative")
	}
	if c.Admin.Enabled && (c.Admin.Port < 0 || c.Admin.Port > 65535) {
		return fmt.Errorf("admin: invalid port %d", c.Admin.Port)
	}
	return nil
}

func validProvider(provider, model string) error {
	if provider != ProviderOpenRouter && provider != ProviderLocal {
		return fmt.Errorf("unknown provider %q", provider)
	}
	if strings.TrimSpace(model) == "" {
		return errors.New("model is required")
	}
	return nil
}
