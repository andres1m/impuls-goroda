package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

const (
	tokenBytes           = 32
	uuidVersionByteIndex = 6
	uuidVariantByteIndex = 8
	uuidVersionMask      = 0x0f
	uuidVersion4Bits     = 0x40
	uuidVariantMask      = 0x3f
	uuidRFC4122Bits      = 0x80
)

func newToken(source io.Reader) (token string, hash [32]byte, err error) {
	if source == nil {
		source = rand.Reader
	}
	raw := make([]byte, tokenBytes)
	if _, err := io.ReadFull(source, raw); err != nil {
		return "", [32]byte{}, fmt.Errorf("read token bytes: %w", err)
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
		return [16]byte{}, fmt.Errorf("read UUID bytes: %w", err)
	}
	id[uuidVersionByteIndex] = (id[uuidVersionByteIndex] & uuidVersionMask) | uuidVersion4Bits
	id[uuidVariantByteIndex] = (id[uuidVariantByteIndex] & uuidVariantMask) | uuidRFC4122Bits
	if id == ([16]byte{}) {
		return [16]byte{}, errors.New("random UUID is empty")
	}
	return id, nil
}
