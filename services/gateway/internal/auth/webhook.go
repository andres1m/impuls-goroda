package auth

import (
	"crypto/subtle"
	"errors"
)

type WebhookVerifier struct {
	secret []byte
}

func NewWebhookVerifier(secret string) (*WebhookVerifier, error) {
	if secret == "" {
		return nil, errors.New("MAX webhook secret is required")
	}
	return &WebhookVerifier{secret: []byte(secret)}, nil
}

func (v *WebhookVerifier) Valid(candidate string) bool {
	value := []byte(candidate)
	return len(value) == len(v.secret) && subtle.ConstantTimeCompare(value, v.secret) == 1
}
