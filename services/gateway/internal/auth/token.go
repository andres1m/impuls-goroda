package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
)

const tokenBytes = 32

func newToken(source io.Reader) (string, [32]byte, error) {
	if source == nil {
		source = rand.Reader
	}
	raw := make([]byte, tokenBytes)
	if _, err := io.ReadFull(source, raw); err != nil {
		return "", [32]byte{}, err
	}
	return base64.RawURLEncoding.EncodeToString(raw), sha256.Sum256(raw), nil
}

func hashToken(token string) ([32]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != tokenBytes || base64.RawURLEncoding.EncodeToString(raw) != token {
		return [32]byte{}, ErrAuthRequired
	}
	return sha256.Sum256(raw), nil
}

func newUUID(source io.Reader) ([16]byte, error) {
	if source == nil {
		source = rand.Reader
	}
	var id [16]byte
	if _, err := io.ReadFull(source, id[:]); err != nil {
		return [16]byte{}, err
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	if id == ([16]byte{}) {
		return [16]byte{}, errors.New("random UUID is empty")
	}
	return id, nil
}
