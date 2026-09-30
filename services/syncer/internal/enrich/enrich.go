package enrich

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/andres1m/impuls-goroda/pkg/ai"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

const (
	attempts         = 2
	maxFailedBatches = 5
)

type Enriched struct {
	Candidate Candidate
	Hash      []byte
	Mask      int64
}

type Store interface {
	Vocabulary(ctx context.Context) ([]Tag, error)
	Candidates(ctx context.Context, city domain.City) ([]Candidate, error)
	// Publish stores the results and adds their tags to the catalog; it reports whether the
	// catalog revision grew.
	Publish(ctx context.Context, city domain.City, model string, done []Enriched, at time.Time) (bool, error)
}

type Summary struct {
	Entities, Enriched, Fallback, Failed, Published int
	FailedTitles                                    []string
	LastError                                       error
}

// refuse counts an entity the model gave nothing for: with tags from the rules it stays as it is,
// without any it is a failure the operator has to see.
func (s *Summary) refuse(c *Candidate) {
	if c.HasTags {
		s.Fallback++
		return
	}
	s.Failed++
	s.FailedTitles = append(s.FailedTitles, c.Title)
}

func Run(
	ctx context.Context,
	store Store,
	model ai.TextModel,
	city domain.City,
	batch int,
	now func() time.Time,
) (Summary, error) {
	vocab, err := store.Vocabulary(ctx)
	if err != nil {
		return Summary{}, err
	}
	all, err := store.Candidates(ctx, city)
	if err != nil {
		return Summary{}, err
	}
	s := Summary{Entities: len(all)}
	var stale []Candidate
	for i := range all {
		if !bytes.Equal(Hash(Input(&all[i])), all[i].StoredHash) || all[i].StoredModel != model.Model() {
			stale = append(stale, all[i])
		}
	}

	failedBatches := 0
	for start := 0; start < len(stale); start += batch {
		chunk := stale[start:min(start+batch, len(stale))]
		var masks map[int]int64
		if failedBatches < maxFailedBatches {
			masks, err = ask(ctx, model, vocab, chunk)
			switch {
			case ctx.Err() != nil:
				return s, ctx.Err()
			case err != nil:
				failedBatches++
				s.LastError = err
			default:
				failedBatches = 0
			}
		}
		var done []Enriched
		for i := range chunk {
			mask, ok := masks[i+1]
			if !ok {
				s.refuse(&chunk[i])
				continue
			}
			done = append(done, Enriched{Candidate: chunk[i], Hash: Hash(Input(&chunk[i])), Mask: mask})
		}
		if len(done) == 0 {
			continue
		}
		published, err := store.Publish(ctx, city, model.Model(), done, now())
		if err != nil {
			return s, fmt.Errorf("publish enrichment: %w", err)
		}
		s.Enriched += len(done)
		if published {
			s.Published++
		}
	}
	return s, nil
}

func ask(ctx context.Context, model ai.TextModel, vocab []Tag, chunk []Candidate) (map[int]int64, error) {
	prompt := BuildPrompt(vocab, chunk)
	var err error
	for range attempts {
		var answer string
		if answer, err = model.Complete(ctx, prompt); err == nil {
			var masks map[int]int64
			if masks, err = Parse(answer, len(chunk), vocab); err == nil {
				return masks, nil
			}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, err
}
