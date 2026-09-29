package resolve

import (
	"context"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/andres1m/impuls-goroda/pkg/ai"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

type Kind int

const (
	Own Kind = iota
	Merge
	Review
)

type Reason string

const (
	ReasonAmbiguous Reason = "ambiguous"
	ReasonNoVector  Reason = "no_vector"
)

type Resolution struct {
	Kind    Kind
	PlaceID uuid.UUID
	Score   float64
	// Known marks a decision made earlier for the same source place key.
	Known  bool
	Reason Reason
}

type Place struct {
	Source          domain.SourceKey
	Key             string
	OwnID           uuid.UUID
	Title           string
	NormalizedTitle string
	Category        string
	Lat, Lon        float64
}

type Candidate struct {
	PlaceID         uuid.UUID
	Title           string
	NormalizedTitle string
	Category        string
	Distance        float64
}

type Data interface {
	// Known maps the source place keys that were resolved before to the place they belong to.
	Known(ctx context.Context, city domain.City, source domain.SourceKey, keys []string) (map[string]uuid.UUID, error)
	Existing(ctx context.Context, city domain.City, ids []uuid.UUID) (map[uuid.UUID]bool, error)
	// Nearby lists active places within Radius that another, non-synthetic source described.
	Nearby(ctx context.Context, city domain.City, source domain.SourceKey, lat, lon float64) ([]Candidate, error)
}

// Resolver decides for each arriving place whether it is one the catalog already has. Embedder may be
// nil: pairs that need a vector then stay separate for review.
type Resolver struct {
	Data     Data
	Embedder ai.Embedder
}

var resolutions = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "syncer_place_resolution_total",
	Help: "Arriving places by how entity resolution settled them.",
}, []string{"result"})

func (r *Resolver) Resolve(ctx context.Context, city domain.City, places []Place) ([]Resolution, error) {
	known, existing, err := r.lookup(ctx, city, places)
	if err != nil {
		return nil, err
	}
	out := make([]Resolution, len(places))
	type placeKey struct {
		source domain.SourceKey
		key    string
	}
	// Records that describe the same place (many events at one venue) get one decision.
	decided := make(map[placeKey]Resolution, len(places))
	for i := range places {
		p := &places[i]
		if prior, again := decided[placeKey{p.Source, p.Key}]; again {
			out[i] = prior
			continue
		}
		switch {
		case p.Source == domain.SyntheticSource:
			out[i] = Resolution{Kind: Own}
		case known[p.Source][p.Key] != uuid.Nil:
			out[i] = Resolution{Kind: Own}
			if target := known[p.Source][p.Key]; target != p.OwnID {
				out[i] = Resolution{Kind: Merge, PlaceID: target, Known: true}
			}
		case existing[p.OwnID]:
			out[i] = Resolution{Kind: Own}
		default:
			cands, nearbyErr := r.Data.Nearby(ctx, city, p.Source, p.Lat, p.Lon)
			if nearbyErr != nil {
				return nil, fmt.Errorf("find places near %s: %w", p.Key, nearbyErr)
			}
			out[i] = r.decide(ctx, p, append(cands, batchMates(places, out, i)...))
		}
		decided[placeKey{p.Source, p.Key}] = out[i]
		count(out[i])
	}
	return out, nil
}

func (r *Resolver) lookup(
	ctx context.Context,
	city domain.City,
	places []Place,
) (known map[domain.SourceKey]map[string]uuid.UUID, existing map[uuid.UUID]bool, err error) {
	keys := make(map[domain.SourceKey][]string)
	var ids []uuid.UUID
	for i := range places {
		if places[i].Source == domain.SyntheticSource {
			continue
		}
		keys[places[i].Source] = append(keys[places[i].Source], places[i].Key)
		ids = append(ids, places[i].OwnID)
	}
	known = make(map[domain.SourceKey]map[string]uuid.UUID, len(keys))
	for source, list := range keys {
		if known[source], err = r.Data.Known(ctx, city, source, list); err != nil {
			return nil, nil, fmt.Errorf("read resolved place keys of %s: %w", source, err)
		}
	}
	if existing, err = r.Data.Existing(ctx, city, ids); err != nil {
		return nil, nil, fmt.Errorf("read existing places: %w", err)
	}
	return known, existing, nil
}

// batchMates are the earlier places of the batch that will get a row of their own; a place can merge
// into one of them although the catalog does not hold it yet.
func batchMates(places []Place, done []Resolution, i int) []Candidate {
	p := &places[i]
	var out []Candidate
	for j := range i {
		m := &places[j]
		if m.Source == p.Source || m.Source == domain.SyntheticSource || done[j].Kind == Merge {
			continue
		}
		if d := haversine(p.Lat, p.Lon, m.Lat, m.Lon); d <= Radius {
			out = append(out, Candidate{PlaceID: m.OwnID, Title: m.Title, NormalizedTitle: m.NormalizedTitle,
				Category: m.Category, Distance: d})
		}
	}
	return out
}

func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const earth = 6371008.8
	rad := math.Pi / 180
	dLat, dLon := (lat2-lat1)*rad, (lon2-lon1)*rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earth * math.Asin(math.Sqrt(a))
}

type reachable struct {
	c       Candidate
	trigram float64
}

func (r *Resolver) decide(ctx context.Context, p *Place, cands []Candidate) Resolution {
	var pairs []reachable
	mine := Clean(p.NormalizedTitle)
	seen := make(map[uuid.UUID]bool, len(cands))
	for i := range cands {
		// The catalog search and the batch can both offer one place, and so can several records of it.
		if seen[cands[i].PlaceID] {
			continue
		}
		seen[cands[i].PlaceID] = true
		tri := Trigram(mine, Clean(cands[i].NormalizedTitle))
		if Reachable(tri, cands[i].Distance) {
			pairs = append(pairs, reachable{cands[i], tri})
		}
	}
	if len(pairs) == 0 {
		return Resolution{Kind: Own}
	}
	vectors, ok := r.embed(ctx, p, pairs)
	if !ok {
		return Resolution{Kind: Review, Reason: ReasonNoVector}
	}
	var best *reachable
	var bestScore float64
	over := 0
	for i := range pairs {
		s := Score(pairs[i].trigram, Cosine(vectors[0], vectors[i+1]), pairs[i].c.Distance)
		if s < Threshold {
			continue
		}
		over++
		if best == nil || s > bestScore {
			best, bestScore = &pairs[i], s
		}
	}
	switch {
	case over == 1:
		return Resolution{Kind: Merge, PlaceID: best.c.PlaceID, Score: bestScore}
	case over > 1:
		return Resolution{Kind: Review, Reason: ReasonAmbiguous}
	}
	return Resolution{Kind: Own}
}

func text(title, category string) string {
	if category == "" {
		return title
	}
	return title + ". " + category
}

// embed returns the vector of the place followed by those of the pairs' candidates.
func (r *Resolver) embed(ctx context.Context, p *Place, pairs []reachable) ([][]float32, bool) {
	if r.Embedder == nil {
		return nil, false
	}
	texts := []string{text(p.Title, p.Category)}
	for i := range pairs {
		texts = append(texts, text(pairs[i].c.Title, pairs[i].c.Category))
	}
	got, err := r.Embedder.Embed(ctx, texts)
	if err != nil || len(got.Vectors) != len(texts) {
		return nil, false
	}
	return got.Vectors, true
}

func count(res Resolution) {
	switch {
	case res.Kind == Merge && !res.Known:
		resolutions.WithLabelValues("merged").Inc()
	case res.Kind == Review:
		resolutions.WithLabelValues(string(res.Reason)).Inc()
	case res.Kind == Own:
		resolutions.WithLabelValues("own").Inc()
	}
}
