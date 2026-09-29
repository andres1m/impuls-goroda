package sharewire

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"time"
	"unicode/utf8"
)

var errInvalidInput = errors.New("invalid share input")

type Token struct {
	value string
}

func ParseToken(value string) (Token, error) {
	if len(value) != 43 {
		return Token{}, errors.New("invalid share token")
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return Token{}, errors.New("invalid share token")
	}
	return Token{value: value}, nil
}

func (t Token) String() string { return "[redacted]" }

func (t Token) Raw() string { return t.value }

func (t Token) Hash() ([32]byte, error) {
	if _, err := ParseToken(t.value); err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256([]byte(t.value)), nil
}

type CreateInput struct {
	Token     Token
	ExpiresAt *time.Time
}

func DecodeCreateInput(raw []byte) (CreateInput, error) {
	if !utf8.Valid(raw) {
		return CreateInput{}, errInvalidInput
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return CreateInput{}, errInvalidInput
	}
	var input CreateInput
	seen := make(map[string]bool, 2)
	for decoder.More() {
		key, err := decoder.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] {
			return CreateInput{}, errInvalidInput
		}
		seen[name] = true
		value, err := decoder.Token()
		text, ok := value.(string)
		if err != nil || !ok {
			return CreateInput{}, errInvalidInput
		}
		switch name {
		case "share_token":
			input.Token, err = ParseToken(text)
		case "expires_at":
			var expiry time.Time
			expiry, err = time.Parse(time.RFC3339Nano, text)
			if err == nil && (expiry.Year() < 1 || expiry.Year() > 9999) {
				err = errInvalidInput
			}
			if err == nil {
				expiry = expiry.UTC()
				input.ExpiresAt = &expiry
			}
		default:
			err = errInvalidInput
		}
		if err != nil {
			return CreateInput{}, errInvalidInput
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') || !seen["share_token"] {
		return CreateInput{}, errInvalidInput
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return CreateInput{}, errInvalidInput
	}
	return input, nil
}
