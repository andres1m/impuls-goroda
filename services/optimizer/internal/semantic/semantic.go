// Package semantic narrows a request's candidates to the catalog entities closest in meaning
// to the user's free-text wishes.
package semantic

import (
	"context"
	"errors"
	"fmt"

	"github.com/andres1m/impuls-goroda/pkg/ai"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

var ErrNoVectors = errors.New("no catalog vectors for the embedding model")

// Match is one catalog entity close to the query; exactly one field is set.
type Match struct {
	Place   *domain.PlaceID
	Event   *domain.EventID
	Session *domain.SessionID
}

type Store interface {
	NearestEntities(ctx context.Context, city string, space ai.Space, vector []float32, limit int) ([]Match, error)
}

type Matches struct {
	places   map[domain.PlaceID]struct{}
	events   map[domain.EventID]struct{}
	sessions map[domain.SessionID]struct{}
}

func NewMatches(list []Match) Matches {
	m := Matches{
		places:   make(map[domain.PlaceID]struct{}),
		events:   make(map[domain.EventID]struct{}),
		sessions: make(map[domain.SessionID]struct{}),
	}
	for _, match := range list {
		switch {
		case match.Place != nil:
			m.places[*match.Place] = struct{}{}
		case match.Event != nil:
			m.events[*match.Event] = struct{}{}
		case match.Session != nil:
			m.sessions[*match.Session] = struct{}{}
		}
	}
	return m
}

// Contains reports whether the candidate's session, event or place matched: a matched place
// brings all of its visits, a matched event all of its sessions.
func (m Matches) Contains(c domain.Candidate) bool {
	if _, ok := m.places[c.Place.ID]; ok {
		return true
	}
	if c.Event != nil {
		if _, ok := m.events[c.Event.ID]; ok {
			return true
		}
	}
	if c.Session != nil {
		if _, ok := m.sessions[c.Session.ID]; ok {
			return true
		}
	}
	return false
}

type Retriever struct {
	embedder ai.Embedder
	store    Store
	limit    int
}

func NewRetriever(embedder ai.Embedder, store Store, limit int) (*Retriever, error) {
	if embedder == nil || store == nil {
		return nil, errors.New("semantic retriever needs an embedder and a store")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("semantic limit must be positive, got %d", limit)
	}
	return &Retriever{embedder: embedder, store: store, limit: limit}, nil
}

func (r *Retriever) Match(ctx context.Context, city, query string) (Matches, error) {
	embedding, err := r.embedder.Embed(ctx, []string{query})
	if err != nil {
		return Matches{}, err
	}
	if len(embedding.Vectors) != 1 {
		return Matches{}, fmt.Errorf("embedder returned %d vectors for one query", len(embedding.Vectors))
	}
	list, err := r.store.NearestEntities(ctx, city, embedding.Space, embedding.Vectors[0], r.limit)
	if err != nil {
		return Matches{}, err
	}
	if len(list) == 0 {
		return Matches{}, ErrNoVectors
	}
	return NewMatches(list), nil
}
