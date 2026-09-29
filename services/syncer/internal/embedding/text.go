// Package embedding prepares catalog places and events for vector search.
package embedding

import (
	"bytes"
	"crypto/sha256"
	"strings"

	"github.com/google/uuid"
)

// Entity is a place or an event (exactly one ID is set) with the words it is searched by.
type Entity struct {
	City      string
	Place     *uuid.UUID
	Event     *uuid.UUID
	Title     string
	Category  string
	Interests []string
	// Hash of the text the stored vector was made from; nil when there is none for the model.
	StoredHash []byte
}

type Prepared struct {
	Entity Entity
	Text   string
	Hash   []byte
}

func Text(e *Entity) string {
	parts := []string{strings.TrimSpace(e.Title)}
	if e.Category != "" {
		parts = append(parts, e.Category)
	}
	if len(e.Interests) > 0 {
		parts = append(parts, "Интересы: "+strings.Join(e.Interests, ", "))
	}
	return strings.Join(parts, ". ")
}

func Hash(text string) []byte {
	sum := sha256.Sum256([]byte(text))
	return sum[:]
}

// Stale keeps the entities without a vector or whose text changed since theirs was made.
func Stale(entities []Entity) []Prepared {
	var out []Prepared
	for i := range entities {
		e := &entities[i]
		text := Text(e)
		hash := Hash(text)
		if bytes.Equal(hash, e.StoredHash) {
			continue
		}
		out = append(out, Prepared{Entity: *e, Text: text, Hash: hash})
	}
	return out
}
