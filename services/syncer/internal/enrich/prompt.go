// Package enrich asks a text model for the interest tags of catalog places and events.
package enrich

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/andres1m/impuls-goroda/pkg/ai"
)

type Tag struct {
	Bit         int
	Code, Title string
}

// Candidate is an active place or event (exactly one ID is set).
type Candidate struct {
	City       string
	Place      *uuid.UUID
	Event      *uuid.UUID
	Title      string
	Category   string
	HasTags    bool
	StoredHash []byte
}

// Input is everything the model sees about the entity, and what the stored hash covers.
func Input(c *Candidate) string {
	title := strings.TrimSpace(c.Title)
	if c.Category == "" {
		return title
	}
	return title + ". " + c.Category
}

func Hash(input string) []byte {
	sum := sha256.Sum256([]byte(input))
	return sum[:]
}

func BuildPrompt(vocab []Tag, batch []Candidate) ai.Prompt {
	var system strings.Builder
	system.WriteString("You assign interest tags to city places and events. Use only the tag codes below; " +
		"give none when none fits. Answer with a JSON array only, one object per item: " +
		"{\"n\": <item number>, \"tags\": [<codes>]}.\nTags:\n")
	for _, t := range vocab {
		fmt.Fprintf(&system, "- %s: %s\n", t.Code, t.Title)
	}
	var user strings.Builder
	for i := range batch {
		fmt.Fprintf(&user, "%d. %s\n", i+1, Input(&batch[i]))
	}
	return ai.Prompt{System: system.String(), User: user.String()}
}

var ErrAnswer = errors.New("unusable model answer")

// Parse returns the tag mask of every well-formed item, keyed by its 1-based number. Codes outside the
// vocabulary are dropped; an item numbered twice is dropped whole, because its owner is unknown.
func Parse(answer string, count int, vocab []Tag) (map[int]int64, error) {
	start, end := strings.Index(answer, "["), strings.LastIndex(answer, "]")
	if start < 0 || end < start {
		return nil, ErrAnswer
	}
	var items []struct {
		N    int      `json:"n"`
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal([]byte(answer[start:end+1]), &items); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAnswer, err)
	}
	bits := make(map[string]int, len(vocab))
	for _, t := range vocab {
		bits[t.Code] = t.Bit
	}
	masks := make(map[int]int64, len(items))
	seen := make(map[int]bool, len(items))
	for _, it := range items {
		if it.N < 1 || it.N > count {
			continue
		}
		if seen[it.N] {
			delete(masks, it.N)
			continue
		}
		seen[it.N] = true
		var mask int64
		for _, code := range it.Tags {
			if bit, ok := bits[code]; ok {
				mask |= 1 << bit
			}
		}
		masks[it.N] = mask
	}
	return masks, nil
}
